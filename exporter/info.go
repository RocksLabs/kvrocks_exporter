package exporter

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func extractVal(s string) (val float64, err error) {
	split := strings.Split(s, "=")
	if len(split) != 2 {
		return 0, fmt.Errorf("nope")
	}
	val, err = strconv.ParseFloat(split[1], 64)
	if err != nil {
		return 0, fmt.Errorf("nope")
	}
	return
}

func (e *Exporter) extractInfoMetrics(ch chan<- prometheus.Metric, info string, dbCount int) {
	keyValues := map[string]string{}
	handledDBs := map[string]bool{}

	fieldClass := ""
	lines := strings.Split(info, "\n")
	masterHost := ""
	masterPort := ""

	linePrefixesToSkip := []string{
		"# Last DBSIZE SCAN",
		"# Last scan db time",
		"# WARN:",
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		log.Debugf("info: %s", line)

		if len(line) > 0 && strings.HasPrefix(line, "# ") {
			skip := false
			for _, skipPrefix := range linePrefixesToSkip {
				if strings.HasPrefix(line, skipPrefix) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}

			fieldClass = line[2:]
			log.Debugf("set fieldClass: %s", fieldClass)
			continue
		}

		if (len(line) < 2) || (!strings.Contains(line, ":")) {
			continue
		}

		index := strings.LastIndexByte(line, ':')
		fieldKey := line[0:index]
		fieldValue := line[index+1:]

		keyValues[fieldKey] = fieldValue

		if fieldKey == "master_host" {
			masterHost = fieldValue
		}

		if fieldKey == "master_port" {
			masterPort = fieldValue
		}

		switch fieldClass {

		case "Replication":
			if ok := e.handleMetricsReplication(ch, masterHost, masterPort, fieldKey, fieldValue); ok {
				continue
			}

		case "Server":
			e.handleMetricsServer(ch, fieldKey, fieldValue)

		case "Commandstats", "CommandStats":
			e.handleMetricsCommandStats(ch, fieldKey, fieldValue)
			continue

		case "Keyspace":
			if keysTotal, keysEx, avgTTL, keysExpired, ok := parseDBKeyspaceString(fieldKey, fieldValue); ok {
				dbName := fieldKey

				e.registerConstMetricGauge(ch, "db_keys", keysTotal, dbName)
				e.registerConstMetricGauge(ch, "db_keys_expiring", keysEx, dbName)
				e.registerConstMetricGauge(ch, "db_keys_expired", keysExpired, dbName)

				if avgTTL > -1 {
					e.registerConstMetricGauge(ch, "db_avg_ttl_seconds", avgTTL, dbName)
				}
				handledDBs[dbName] = true
				continue
			}
		case "RocksDB":
			e.handleMetricsRocksDB(ch, fieldKey, fieldValue)
		}

		if !e.includeMetric(fieldKey) {
			continue
		}

		e.parseAndRegisterConstMetric(ch, fieldKey, fieldValue)
	}

	for dbIndex := 0; dbIndex < dbCount; dbIndex++ {
		dbName := "db" + strconv.Itoa(dbIndex)
		if _, exists := handledDBs[dbName]; !exists {
			e.registerConstMetricGauge(ch, "db_keys", 0, dbName)
			e.registerConstMetricGauge(ch, "db_keys_expiring", 0, dbName)
			e.registerConstMetricGauge(ch, "db_keys_expired", 0, dbName)
		}
	}

	e.registerConstMetricGauge(ch, "instance_info", 1,
		keyValues["role"],
		keyValues["kvrocks_version"],
		keyValues["kvrocks_git_sha1"],
		keyValues["os"],
		keyValues["tcp_port"],
		keyValues["gcc_version"],
		keyValues["process_id"],
	)

	if keyValues["role"] == "slave" {
		e.registerConstMetricGauge(ch, "slave_info", 1,
			keyValues["master_host"],
			keyValues["master_port"],
			keyValues["slave_read_only"])
	}
}

/*
valid examples:
  - db0:keys=1,expires=0,avg_ttl=0
  - db0:keys=1,expires=10,avg_ttl=0,expired=2
*/
func parseDBKeyspaceString(inputKey string, inputVal string) (keysTotal float64, keysExpiringTotal float64, avgTTL float64, keysExpiredTotal float64, ok bool) {
	log.Debugf("parseDBKeyspaceString inputKey: [%s] inputVal: [%s]", inputKey, inputVal)

	if !strings.HasPrefix(inputKey, "db") {
		log.Debugf("parseDBKeyspaceString inputKey not starting with 'db': [%s]", inputKey)
		return
	}

	split := strings.Split(inputVal, ",")
	if len(split) < 2 || len(split) > 4 {
		log.Debugf("parseDBKeyspaceString strings.Split(inputVal) invalid: %#v", split)
		return
	}

	var err error
	if keysTotal, err = extractVal(split[0]); err != nil {
		log.Debugf("parseDBKeyspaceString extractVal(split[0]) invalid, err: %s", err)
		return
	}
	if keysExpiringTotal, err = extractVal(split[1]); err != nil {
		log.Debugf("parseDBKeyspaceString extractVal(split[1]) invalid, err: %s", err)
		return
	}

	avgTTL = -1
	if len(split) > 2 {
		if avgTTL, err = extractVal(split[2]); err != nil {
			log.Debugf("parseDBKeyspaceString extractVal(split[2]) invalid, err: %s", err)
			return
		}
		avgTTL /= 1000
	}

	keysExpiredTotal = 0
	if len(split) > 3 {
		if keysExpiredTotal, err = extractVal(split[3]); err != nil {
			log.Debugf("parseDBKeyspaceString extractVal(split[3]) invalid, err: %s", err)
			return
		}
	}

	ok = true
	return
}

/*
slave0:ip=10.254.11.1,port=6379,state=online,offset=1751844676,lag=0
slave1:ip=10.254.11.2,port=6379,state=online,offset=1751844222,lag=0
*/
func parseConnectedSlaveString(slaveName string, keyValues string) (offset float64, ip string, port string, state string, lag float64, ok bool) {
	ok = false
	if matched, _ := regexp.MatchString(`^slave\d+`, slaveName); !matched {
		return
	}
	connectedkeyValues := make(map[string]string)
	for _, kvPart := range strings.Split(keyValues, ",") {
		x := strings.Split(kvPart, "=")
		if len(x) != 2 {
			log.Debugf("Invalid format for connected slave string, got: %s", kvPart)
			return
		}
		connectedkeyValues[x[0]] = x[1]
	}
	offset, err := strconv.ParseFloat(connectedkeyValues["offset"], 64)
	if err != nil {
		log.Debugf("Can not parse connected slave offset, got: %s", connectedkeyValues["offset"])
		return
	}

	if lagStr, exists := connectedkeyValues["lag"]; !exists {
		// Prior to Redis 3.0, "lag" property does not exist
		lag = -1
	} else {
		lag, err = strconv.ParseFloat(lagStr, 64)
		if err != nil {
			log.Debugf("Can not parse connected slave lag, got: %s", lagStr)
			return
		}
	}

	ok = true
	ip = connectedkeyValues["ip"]
	port = connectedkeyValues["port"]
	state = connectedkeyValues["state"]

	return
}

func (e *Exporter) handleMetricsReplication(ch chan<- prometheus.Metric, masterHost string, masterPort string, fieldKey string, fieldValue string) bool {
	// only slaves have this field
	if fieldKey == "master_link_status" {
		if fieldValue == "up" {
			e.registerConstMetricGauge(ch, "master_link_up", 1, masterHost, masterPort)
		} else {
			e.registerConstMetricGauge(ch, "master_link_up", 0, masterHost, masterPort)
		}
		return true
	}
	switch fieldKey {

	case "master_last_io_seconds_ago", "slave_repl_offset", "master_sync_in_progress":
		val, _ := strconv.Atoi(fieldValue)
		e.registerConstMetricGauge(ch, fieldKey, float64(val), masterHost, masterPort)
		return true
	}

	// not a slave, try extracting master metrics
	if slaveOffset, slaveIP, slavePort, slaveState, slaveLag, ok := parseConnectedSlaveString(fieldKey, fieldValue); ok {
		e.registerConstMetricGauge(ch,
			"connected_slave_offset_bytes",
			slaveOffset,
			slaveIP, slavePort, slaveState,
		)

		if slaveLag > -1 {
			e.registerConstMetricGauge(ch,
				"connected_slave_lag_seconds",
				slaveLag,
				slaveIP, slavePort, slaveState,
			)
		}
		return true
	}

	return false
}

func (e *Exporter) handleMetricsRocksDB(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
	sharedMetric := []string{"block_cache_usage"}
	for _, field := range sharedMetric {
		// format like `block_cache_usage:0`
		if strings.Compare(fieldKey, field) == 0 {
			if statValue, err := strconv.ParseFloat(fieldValue, 64); err == nil {
				e.registerConstMetricGauge(ch, fieldKey, statValue, "-")
			}
			// return ASAP
			return
		}
	}

	prefixs := []string{
		"block_cache_usage", "block_cache_pinned_usage", "index_and_filter_cache_usage", "estimate_keys",
		"level0_file_limit_slowdown", "level0_file_limit_stop", "pending_compaction_bytes_slowdown",
		"pending_compaction_bytes_stop", "memtable_count_limit_slowdown", "memtable_count_limit_stop",
	}
	for _, prefix := range prefixs {
		// format like `estimate_keys[default]:0`
		if strings.HasPrefix(fieldKey, prefix) {
			fields := strings.Split(fieldKey, "[")
			if len(fields) != 2 {
				continue
			}
			metricName := strings.TrimRight(fields[0], ":")
			columnFamily := strings.TrimRight(fields[1], "]")
			if statValue, err := strconv.ParseFloat(fieldValue, 64); err == nil {
				e.registerConstMetricGauge(ch, metricName, statValue, columnFamily)
			}
			return
		}
	}
}

func (e *Exporter) handleMetricsServer(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
	if fieldKey == "uptime_in_seconds" {
		if uptime, err := strconv.ParseFloat(fieldValue, 64); err == nil {
			e.registerConstMetricGauge(ch, "start_time_seconds", float64(time.Now().Unix())-uptime)
		}
	}
}

func parseMetricsCommandStats(fieldKey string, fieldValue string) (string, float64, float64, error) {
	/*
		Format:
		cmdstat_get:calls=21,usec=175,usec_per_call=8.33
		cmdstat_set:calls=61,usec=3139,usec_per_call=51.46
		cmdstat_setex:calls=75,usec=1260,usec_per_call=16.80
		cmdstat_georadius_ro:calls=75,usec=1260,usec_per_call=16.80

		broken up like this:
			fieldKey  = cmdstat_get
			fieldValue= calls=21,usec=175,usec_per_call=8.33
	*/

	const cmdPrefix = "cmdstat_"

	if !strings.HasPrefix(fieldKey, cmdPrefix) {
		return "", 0.0, 0.0, errors.New("Invalid fieldKey")
	}
	cmd := strings.TrimPrefix(fieldKey, cmdPrefix)

	splitValue := strings.Split(fieldValue, ",")
	if len(splitValue) < 3 {
		return "", 0.0, 0.0, errors.New("Invalid fieldValue")
	}

	calls, err := extractVal(splitValue[0])
	if err != nil {
		return "", 0.0, 0.0, errors.New("Invalid splitValue[0]")
	}

	usecTotal, err := extractVal(splitValue[1])
	if err != nil {
		return "", 0.0, 0.0, errors.New("Invalid splitValue[1]")
	}

	return cmd, calls, usecTotal, nil
}

func histSplit(r rune) bool {
	return r == '=' || r == ','
}

func parseMetricsCommandStatsHist(fieldKey string, fieldValue string) (string, uint64, uint64, map[float64]uint64, error) {
	/*
		Format:
		cmdstathist_get:10=1191,20=1,50=0,70=0,100=0,150=0,inf=0,sum=12388,count=1192

		broken up like this:
			fieldKey = cmdstathist_get
			fieldValue = 10=1191,20=1,50=0,70=0,100=0,150=0,inf=0,sum=12388,count=1192
	*/

	const cmdPrefix = "cmdstathist_"

	if !strings.HasPrefix(fieldKey, cmdPrefix) {
		return "", 0, 0, nil, errors.New("invalid fieldKey")
	}
	cmd := strings.TrimPrefix(fieldKey, cmdPrefix)

	splitValues := strings.FieldsFunc(fieldValue, histSplit)
	var histogram = map[float64]uint64{}
	var keys = make([]float64, 0, len(histogram))

	if len(splitValues)%2 != 0 {
		return "", 0, 0, nil, errors.New("uneven number of keys for bucket")
	}

	var sum, count uint64
	var err error
	// NB: splitValues slice is a list of tuples so iterating by 2
	for i := 0; i < len(splitValues); i = i + 2 {
		if splitValues[i] == "sum" {
			sum, err = strconv.ParseUint(splitValues[i+1], 10, 64)
			if err != nil {
				return "", 0, 0, nil, fmt.Errorf("invalid value for sum: %w", err)
			}
			continue
		}
		if splitValues[i] == "count" {
			count, err = strconv.ParseUint(splitValues[i+1], 10, 64)
			if err != nil {
				return "", 0, 0, nil, fmt.Errorf("invalid value for count: %w", err)
			}
			continue
		}

		bucketCount, err := strconv.ParseUint(splitValues[i+1], 10, 64)
		if err != nil {
			return "", 0, 0, nil, fmt.Errorf("invalid splitValue for bucket: %w", err)
		}
		bucketValue := math.Inf(1)
		if val, err := strconv.ParseFloat(strings.TrimSpace(splitValues[i]), 64); err == nil {
			bucketValue = val / 1e6
		}
		histogram[bucketValue] = bucketCount
		keys = append(keys, bucketValue)
	}

	sort.Float64s(keys)

	for i := 1; i < len(keys); i++ {
		histogram[keys[i]] += histogram[keys[i-1]]
	}
	return cmd, count, sum, histogram, nil
}

func (e *Exporter) handleMetricsCommandStats(ch chan<- prometheus.Metric, fieldKey string, fieldValue string) {
	if cmd, calls, usecTotal, err := parseMetricsCommandStats(fieldKey, fieldValue); err == nil {
		e.registerConstMetric(ch, "commands_total", calls, prometheus.CounterValue, cmd)
		e.registerConstMetric(ch, "commands_duration_seconds_total", usecTotal/1e6, prometheus.CounterValue, cmd)
	}
	if cmd, count, sum, buckets, err := parseMetricsCommandStatsHist(fieldKey, fieldValue); err == nil {
		e.registerHist(ch, "commands_duration_seconds_bucket", count, float64(sum)/1e6, buckets, cmd)
	}
}
