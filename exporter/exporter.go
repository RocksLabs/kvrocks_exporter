package exporter

import (
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	log "github.com/sirupsen/logrus"
)

type BuildInfo struct {
	Version   string
	CommitSha string
	Date      string
}

type Exporter struct {
	sync.Mutex

	kvrocksAddr string
	namespace   string

	totalScrapes              prometheus.Counter
	scrapeDuration            prometheus.Summary
	targetScrapeRequestErrors prometheus.Counter

	metricDescriptions map[string]*prometheus.Desc

	options Options

	metricMapCounters map[string]string
	metricMapGauges   map[string]string

	mux *http.ServeMux

	buildInfo BuildInfo
}

type Options struct {
	Password              string
	Namespace             string
	PasswordMap           map[string]string
	ConfigCommandName     string
	ClientCertFile        string
	ClientKeyFile         string
	CaCertFile            string
	InclSystemMetrics     bool
	SkipTLSVerification   bool
	SetClientName         bool
	IsCluster             bool
	ExportClientsInclPort bool
	ConnectionTimeouts    time.Duration
	MetricsPath           string
	KvrocksMetricsOnly    bool
	PingOnConnect         bool
	Registry              *prometheus.Registry
	BuildInfo             BuildInfo
}

// NewKvrocksExporter returns a new exporter of Kvrocks metrics.
func NewKvrocksExporter(kvrocksURI string, opts Options) (*Exporter, error) {
	log.Debugf("NewKvrocksExporter options: %#v", opts)

	e := &Exporter{
		kvrocksAddr: kvrocksURI,
		options:     opts,
		namespace:   opts.Namespace,

		buildInfo: opts.BuildInfo,

		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: opts.Namespace,
			Name:      "exporter_scrapes_total",
			Help:      "Current total kvrocks scrapes.",
		}),

		scrapeDuration: prometheus.NewSummary(prometheus.SummaryOpts{
			Namespace: opts.Namespace,
			Name:      "exporter_scrape_duration_seconds",
			Help:      "Durations of scrapes by the exporter",
		}),

		targetScrapeRequestErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: opts.Namespace,
			Name:      "target_scrape_request_errors_total",
			Help:      "Errors in requests to the exporter",
		}),

		metricMapGauges: map[string]string{
			// # Server
			"uptime_in_seconds": "uptime_in_seconds",
			"process_id":        "process_id",

			// # Clients
			"connected_clients": "connected_clients",
			"blocked_clients":   "blocked_clients",
			"monitor_clients":   "monitor_clients",

			// # Memory
			"used_memory":     "memory_used_bytes",
			"used_memory_rss": "memory_used_rss_bytes",
			"used_memory_lua": "memory_used_lua_bytes",

			// # Persistence
			"loading": "loading",

			// # Stats
			"pubsub_channels": "pubsub_channels",
			"pubsub_patterns": "pubsub_patterns",
			"keyspace_hits":   "keyspace_hits",
			"keyspace_misses": "keyspace_misses",

			// # Replication
			"connected_slaves":   "connected_slaves",
			"master_repl_offset": "master_repl_offset",
			"sync_full":          "replica_resyncs_full",
			"sync_partial_ok":    "replica_partial_resync_accepted",
			"sync_partial_err":   "replica_partial_resync_denied",

			// # Keyspace
			"sequence":       "sequence",
			"used_db_size":   "used_db_size",
			"max_db_size":    "max_db_size",
			"disk_capacity":  "disk_capacity_bytes",
			"used_disk_size": "used_disk_size",

			// # RocksDB
			"all_mem_tables":            "all_mem_tables",
			"cur_mem_tables":            "cur_mem_tables",
			"snapshots":                 "snapshots",
			"num_immutable_tables":      "num_immutable_tables",
			"num_running_flushes":       "num_running_flushes",
			"memtable_flush_pending":    "memtable_flush_pending",
			"compaction_pending":        "compaction_pending",
			"num_running_compactions":   "num_running_compactions",
			"num_live_versions":         "num_live_versions",
			"num_superversion":          "num_superversion",
			"num_background_errors":     "num_background_errors",
			"flush_count":               "flush_count",
			"compaction_count":          "compaction_count",
			"instantaneous_ops_per_sec": "instantaneous_ops_per_sec",
			"is_bgsaving":               "is_bgsaving",
			"is_compacting":             "is_compacting",
			"put_per_sec":               "put_per_sec",
			"get_per_sec":               "get_per_sec",
			"seek_per_sec":              "seek_per_sec",
			"next_per_sec":              "next_per_sec",
			"prev_per_sec":              "prev_per_sec",
		},

		metricMapCounters: map[string]string{
			"total_connections_received": "connections_received_total",
			"total_commands_processed":   "commands_processed_total",

			"rejected_connections":   "rejected_connections_total",
			"total_net_input_bytes":  "net_input_bytes_total",
			"total_net_output_bytes": "net_output_bytes_total",

			"used_cpu_sys":  "cpu_sys_seconds_total",
			"used_cpu_user": "cpu_user_seconds_total",
		},
	}

	if e.options.ConfigCommandName == "" {
		e.options.ConfigCommandName = "CONFIG"
	}

	e.metricMapGauges["total_system_memory"] = "total_system_memory_bytes"

	e.metricDescriptions = map[string]*prometheus.Desc{}

	for k, desc := range map[string]struct {
		txt  string
		lbls []string
	}{
		"commands_duration_seconds_total":      {txt: `Total amount of time in seconds spent per command`, lbls: []string{"cmd"}},
		"commands_total":                       {txt: `Total number of calls per command`, lbls: []string{"cmd"}},
		"connected_slave_lag_seconds":          {txt: "Lag of connected slave", lbls: []string{"slave_ip", "slave_port", "slave_state"}},
		"connected_slave_offset_bytes":         {txt: "Offset of connected slave", lbls: []string{"slave_ip", "slave_port", "slave_state"}},
		"db_avg_ttl_seconds":                   {txt: "Avg TTL in seconds", lbls: []string{"db"}},
		"db_keys":                              {txt: "Total number of keys by DB", lbls: []string{"db"}},
		"db_keys_expiring":                     {txt: "Total number of expiring keys by DB", lbls: []string{"db"}},
		"db_keys_expired":                      {txt: "Total number of expired keys by DB", lbls: []string{"db"}},
		"exporter_last_scrape_error":           {txt: "The last scrape error status.", lbls: []string{"err"}},
		"instance_info":                        {txt: "Information about the kvrocks instance", lbls: []string{"role", "version", "git_sha1", "os", "tcp_port", "gcc_version", "process_id"}},
		"last_slow_execution_duration_seconds": {txt: `The amount of time needed for last slow execution, in seconds`},
		"latency_spike_last":                   {txt: `When the latency spike last occurred`, lbls: []string{"event_name"}},
		"latency_spike_duration_seconds":       {txt: `Length of the last latency spike in seconds`, lbls: []string{"event_name"}},
		"master_link_up":                       {txt: "Master link status on Kvrocks slave", lbls: []string{"master_host", "master_port"}},
		"master_sync_in_progress":              {txt: "Master sync in progress", lbls: []string{"master_host", "master_port"}},
		"master_last_io_seconds_ago":           {txt: "Master last io seconds ago", lbls: []string{"master_host", "master_port"}},
		"slave_repl_offset":                    {txt: "Slave replication offset", lbls: []string{"master_host", "master_port"}},
		"slave_info":                           {txt: "Information about the Kvrocks slave", lbls: []string{"master_host", "master_port", "read_only"}},
		"slowlog_last_id":                      {txt: `Last id of slowlog`},
		"slowlog_length":                       {txt: `Total slowlog`},
		"start_time_seconds":                   {txt: "Start time of the kvrocks instance since unix epoch in seconds."},
		"up":                                   {txt: "Information about the kvrocks instance"},

		"index_and_filter_cache_usage": {txt: `The number of bytes used by the index and filter block cache`, lbls: []string{"column_family"}},
		"block_cache_pinned_usage":     {txt: `The number of bytes used by the pinned block cache`, lbls: []string{"column_family"}},
		"block_cache_usage":            {txt: `The number of bytes used by the data block cache`, lbls: []string{"column_family"}},
		"estimate_keys":                {txt: `The estimate keys`, lbls: []string{"column_family"}},
	} {
		e.metricDescriptions[k] = newMetricDescr(opts.Namespace, k, desc.txt, desc.lbls)
	}

	if e.options.MetricsPath == "" {
		e.options.MetricsPath = "/metrics"
	}

	e.mux = http.NewServeMux()

	if e.options.Registry != nil {
		e.options.Registry.MustRegister(e)
		e.mux.Handle(e.options.MetricsPath, promhttp.HandlerFor(
			e.options.Registry, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError},
		))

		if !e.options.KvrocksMetricsOnly {
			buildInfoCollector := prometheus.NewGaugeVec(prometheus.GaugeOpts{
				Namespace: opts.Namespace,
				Name:      "exporter_build_info",
				Help:      "kvrocks exporter build_info",
			}, []string{"version", "commit_sha", "build_date", "golang_version"})
			buildInfoCollector.WithLabelValues(e.buildInfo.Version, e.buildInfo.CommitSha, e.buildInfo.Date, runtime.Version()).Set(1)
			e.options.Registry.MustRegister(buildInfoCollector)
		}
	}

	e.mux.HandleFunc("/", e.indexHandler)
	e.mux.HandleFunc("/scrape", e.scrapeHandler)
	e.mux.HandleFunc("/health", e.healthHandler)

	return e, nil
}

// Describe outputs Redis metric descriptions.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range e.metricDescriptions {
		ch <- desc
	}

	for _, v := range e.metricMapGauges {
		ch <- newMetricDescr(e.options.Namespace, v, v+" metric", nil)
	}

	for _, v := range e.metricMapCounters {
		ch <- newMetricDescr(e.options.Namespace, v, v+" metric", nil)
	}

	ch <- e.totalScrapes.Desc()
	ch <- e.scrapeDuration.Desc()
	ch <- e.targetScrapeRequestErrors.Desc()
}

// Collect fetches new metrics from the KvrocksHost and updates the appropriate metrics.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.Lock()
	defer e.Unlock()
	e.totalScrapes.Inc()

	if e.kvrocksAddr != "" {
		startTime := time.Now()
		var up float64
		if err := e.scrapeKvrocksHost(ch); err != nil {
			e.registerConstMetricGauge(ch, "exporter_last_scrape_error", 1.0, fmt.Sprintf("%s", err))
		} else {
			up = 1
			e.registerConstMetricGauge(ch, "exporter_last_scrape_error", 0, "")
		}

		e.registerConstMetricGauge(ch, "up", up)

		took := time.Since(startTime).Seconds()
		e.scrapeDuration.Observe(took)
		e.registerConstMetricGauge(ch, "exporter_last_scrape_duration_seconds", took)
	}

	ch <- e.totalScrapes
	ch <- e.scrapeDuration
	ch <- e.targetScrapeRequestErrors
}

func (e *Exporter) extractConfigMetrics(ch chan<- prometheus.Metric, config []string) (dbCount int, err error) {
	if len(config)%2 != 0 {
		return 0, fmt.Errorf("invalid config: %#v", config)
	}

	for pos := 0; pos < len(config)/2; pos++ {
		strKey := config[pos*2]
		strVal := config[pos*2+1]
		// todo: we can add more configs to this map if there's interest
		if !map[string]bool{
			"maxclients": true,
		}[strKey] {
			continue
		}

		if val, err := strconv.ParseFloat(strVal, 64); err == nil {
			strKey = strings.ReplaceAll(strKey, "-", "_")
			e.registerConstMetricGauge(ch, fmt.Sprintf("config_%s", strKey), val)
		}
	}
	return
}

func (e *Exporter) scrapeKvrocksHost(ch chan<- prometheus.Metric) error {
	defer log.Debugf("scrapeKvrocksHost() done")

	startTime := time.Now()
	c, err := e.connectToKvrocks()
	connectTookSeconds := time.Since(startTime).Seconds()
	e.registerConstMetricGauge(ch, "exporter_last_scrape_connect_time_seconds", connectTookSeconds)

	if err != nil {
		log.Errorf("Couldn't connect to kvrocks instance")
		log.Debugf("connectToKvrocks( %s ) err: %s", e.kvrocksAddr, err)
		return err
	}
	defer c.Close()

	log.Debugf("connected to: %s", e.kvrocksAddr)
	log.Debugf("connecting took %f seconds", connectTookSeconds)

	if e.options.PingOnConnect {
		startTime := time.Now()

		if _, err := doRedisCmd(c, "PING"); err != nil {
			log.Errorf("Couldn't PING server, err: %s", err)
		} else {
			pingTookSeconds := time.Since(startTime).Seconds()
			e.registerConstMetricGauge(ch, "exporter_last_scrape_ping_time_seconds", pingTookSeconds)
			log.Debugf("PING took %f seconds", pingTookSeconds)
		}
	}

	if e.options.SetClientName {
		if _, err := doRedisCmd(c, "CLIENT", "SETNAME", "kvrocks_exporter"); err != nil {
			log.Errorf("Couldn't set client name, err: %s", err)
		}
	}

	if config, err := redis.Strings(doRedisCmd(c, e.options.ConfigCommandName, "GET", "*")); err == nil {
		log.Debugf("Kvrocks CONFIG GET * result: [%#v]", config)
		_, err = e.extractConfigMetrics(ch, config)
		if err != nil {
			log.Errorf("Kvrocks CONFIG err: %s", err)
			return err
		}
	} else {
		log.Debugf("Kvrocks CONFIG err: %s", err)
	}

	infoAll, err := redis.String(doRedisCmd(c, "INFO", "ALL"))
	if err != nil || infoAll == "" {
		log.Debugf("Kvrocks INFO ALL err: %s", err)
		infoAll, err = redis.String(doRedisCmd(c, "INFO"))
		if err != nil {
			log.Errorf("Kvrocks INFO err: %s", err)
			return err
		}
	}
	e.extractInfoMetrics(ch, infoAll, 1)
	log.Debugf("Kvrocks INFO ALL result: [%#v]", infoAll)
	e.extractSlowLogMetrics(ch, c)
	return nil
}
