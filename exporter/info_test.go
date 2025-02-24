package exporter

import (
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func TestKeyspaceStringParser(t *testing.T) {
	tsts := []struct {
		db                                     string
		stats                                  string
		keysTotal, keysEx, avgTTL, keysExpired float64
		ok                                     bool
	}{
		{db: "xxx", stats: "", ok: false},
		{db: "xxx", stats: "keys=1,expires=0,avg_ttl=0", ok: false},
		{db: "db0", stats: "xxx", ok: false},
		{db: "db1", stats: "keys=abcd,expires=0,avg_ttl=0", ok: false},
		{db: "db2", stats: "keys=1234=1234,expires=0,avg_ttl=0", ok: false},

		{db: "db3", stats: "keys=abcde,expires=0", ok: false},
		{db: "db3", stats: "keys=213,expires=xxx", ok: false},
		{db: "db3", stats: "keys=123,expires=0,avg_ttl=zzz", ok: false},
		{db: "db0", stats: "keys=22113592,expires=21683101,avg_ttl=3816396,expired=340250", keysTotal: 22113592, keysEx: 21683101, avgTTL: 3816.396, keysExpired: 340250, ok: true},
		{db: "db0", stats: "keys=1,expires=0,avg_ttl=0", keysTotal: 1, keysEx: 0, avgTTL: 0, keysExpired: 0, ok: true},
	}

	for _, tst := range tsts {
		if kt, kx, ttl, kexp, ok := parseDBKeyspaceString(tst.db, tst.stats); true {

			if ok != tst.ok {
				t.Errorf("failed for: db:%s stats:%s", tst.db, tst.stats)
				continue
			}

			if ok && (kt != tst.keysTotal || kx != tst.keysEx || ttl != tst.avgTTL || kexp != tst.keysExpired) {
				t.Errorf("values not matching, db:%s stats:%s   %f %f %f %f", tst.db, tst.stats, kt, kx, ttl, kexp)
			}
		}
	}
}

type slaveData struct {
	k, v            string
	ip, state, port string
	offset          float64
	lag             float64
	ok              bool
}

func TestParseConnectedSlaveString(t *testing.T) {
	tsts := []slaveData{
		{k: "slave0", v: "ip=10.254.11.1,port=6379,state=online,offset=1751844676,lag=0", offset: 1751844676, ip: "10.254.11.1", port: "6379", state: "online", ok: true, lag: 0},
		{k: "slave0", v: "ip=2a00:1450:400e:808::200e,port=6379,state=online,offset=1751844676,lag=0", offset: 1751844676, ip: "2a00:1450:400e:808::200e", port: "6379", state: "online", ok: true, lag: 0},
		{k: "slave1", v: "offset=1,lag=0", offset: 1, ok: true},
		{k: "slave1", v: "offset=1", offset: 1, ok: true, lag: -1},
		{k: "slave2", v: "ip=1.2.3.4,state=online,offset=123,lag=42", offset: 123, ip: "1.2.3.4", state: "online", ok: true, lag: 42},

		{k: "slave", v: "offset=1751844676,lag=0", ok: false},
		{k: "slaveA", v: "offset=1751844676,lag=0", ok: false},
		{k: "slave0", v: "offset=abc,lag=0", ok: false},
		{k: "slave0", v: "offset=0,lag=abc", ok: false},
	}

	for _, tst := range tsts {
		t.Run(fmt.Sprintf("%s---%s", tst.k, tst.v), func(t *testing.T) {
			offset, ip, port, state, lag, ok := parseConnectedSlaveString(tst.k, tst.v)

			if ok != tst.ok {
				t.Errorf("failed for: db:%s stats:%s", tst.k, tst.v)
				return
			}
			if offset != tst.offset || ip != tst.ip || port != tst.port || state != tst.state || lag != tst.lag {
				t.Errorf("values not matching, string:%s %f %s %s %s %f", tst.v, offset, ip, port, state, lag)
			}
		})
	}
}

func TestCommandStats(t *testing.T) {
	e := getTestExporter()

	setupDBKeys(t, os.Getenv("TEST_REDIS_URI"))
	defer deleteKeysFromDB(t, os.Getenv("TEST_REDIS_URI"))

	chM := make(chan prometheus.Metric)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	want := map[string]bool{"test_commands_duration_seconds_total": false, "test_commands_total": false}

	for m := range chM {
		for k := range want {
			if strings.Contains(m.Desc().String(), k) {
				want[k] = true
			}
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("didn't find %s", k)
		}
	}
}

func TestClusterMaster(t *testing.T) {
	if os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI") == "" {
		t.Skipf("TEST_REDIS_CLUSTER_MASTER_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_CLUSTER_MASTER_URI")
	e, _ := NewKvrocksExporter(addr, Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	body := downloadURL(t, ts.URL+"/metrics")
	log.Debugf("master - body: %s", body)
	for _, want := range []string{
		"test_instance_info{",
		"test_master_repl_offset",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Did not find key [%s] \nbody: %s", want, body)
		}
	}
}

func TestClusterSlave(t *testing.T) {
	if os.Getenv("TEST_REDIS_CLUSTER_SLAVE_URI") == "" {
		t.Skipf("TEST_REDIS_CLUSTER_SLAVE_URI not set - skipping")
	}

	addr := os.Getenv("TEST_REDIS_CLUSTER_SLAVE_URI")
	e, _ := NewKvrocksExporter(addr, Options{Namespace: "test", Registry: prometheus.NewRegistry()})
	ts := httptest.NewServer(e)
	defer ts.Close()

	chM := make(chan prometheus.Metric, 10000)
	go func() {
		e.Collect(chM)
		close(chM)
	}()

	body := downloadURL(t, ts.URL+"/metrics")
	log.Debugf("slave - body: %s", body)
	for _, want := range []string{
		"test_instance_info",
		"test_master_last_io_seconds",
		"test_slave_info",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Did not find key [%s] \nbody: %s", want, body)
		}
	}
	hostReg, _ := regexp.Compile(`master_host="([0,1]?\d{1,2}|2([0-4][0-9]|5[0-5]))(\.([0,1]?\d{1,2}|2([0-4][0-9]|5[0-5]))){3}"`)
	masterHost := hostReg.FindString(string(body))
	portReg, _ := regexp.Compile(`master_port="(\d+)"`)
	masterPort := portReg.FindString(string(body))
	for wantedKey, wantedVal := range map[string]int{
		masterHost: 5,
		masterPort: 5,
	} {
		if res := strings.Count(body, wantedKey); res != wantedVal {
			t.Errorf("Result: %s -> %d, Wanted: %d \nbody: %s", wantedKey, res, wantedVal, body)
		}
	}
}

func TestParseCommandStats(t *testing.T) {

	for _, tst := range []struct {
		fieldKey   string
		fieldValue string

		wantSuccess   bool
		wantCmd       string
		wantCalls     float64
		wantUsecTotal float64
	}{
		{
			fieldKey:      "cmdstat_get",
			fieldValue:    "calls=21,usec=175,usec_per_call=8.33",
			wantSuccess:   true,
			wantCmd:       "get",
			wantCalls:     21,
			wantUsecTotal: 175,
		},
		{
			fieldKey:      "cmdstat_georadius_ro",
			fieldValue:    "calls=75,usec=1260,usec_per_call=16.80",
			wantSuccess:   true,
			wantCmd:       "georadius_ro",
			wantCalls:     75,
			wantUsecTotal: 1260,
		},
		{
			fieldKey:    "borked_stats",
			fieldValue:  "calls=75,usec=1260,usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "borked_values",
			wantSuccess: false,
		},

		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "calls=ABC,usec=1260,usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "calls=75,usec=DEF,usec_per_call=16.80",
			wantSuccess: false,
		},
		{
			fieldKey:    "cmdstat_georadius_ro",
			fieldValue:  "calls=75,usec=DEF,usec_per_call=16.80",
			wantSuccess: false,
		},
	} {
		t.Run(tst.fieldKey+tst.fieldValue, func(t *testing.T) {

			cmd, calls, usecTotal, err := parseMetricsCommandStats(tst.fieldKey, tst.fieldValue)

			if tst.wantSuccess && err != nil {
				t.Fatalf("err: %s", err)
				return
			}

			if !tst.wantSuccess && err == nil {
				t.Fatalf("expected err!")
				return
			}

			if !tst.wantSuccess {
				return
			}

			if cmd != tst.wantCmd {
				t.Fatalf("cmd not matching, got: %s, wanted: %s", cmd, tst.wantCmd)
			}

			if calls != tst.wantCalls {
				t.Fatalf("cmd not matching, got: %f, wanted: %f", calls, tst.wantCalls)
			}
			if usecTotal != tst.wantUsecTotal {
				t.Fatalf("cmd not matching, got: %f, wanted: %f", usecTotal, tst.wantUsecTotal)
			}
		})
	}
}

func TestParseCommandStatsHist(t *testing.T) {

	for _, tst := range []struct {
		fieldKey   string
		fieldValue string

		wantSuccess bool
		wantCmd     string
		wantBuckets map[float64]uint64
		wantSum     uint64
		wantCount   uint64
	}{
		{
			fieldKey:    "cmdstathist_get",
			fieldValue:  "10=1191,20=1,50=0,70=0,100=0,150=0,inf=0,sum=10000,count=1192",
			wantSuccess: true,
			wantCmd:     "get",
			wantBuckets: map[float64]uint64{
				0.00001:     1191,
				0.00002:     1192,
				0.00005:     1192,
				0.00007:     1192,
				0.0001:      1192,
				0.00015:     1192,
				math.Inf(1): 1192,
			},
			wantSum:   10000,
			wantCount: 1192,
		},
		{
			fieldKey:    "cmdstathist_hget",
			fieldValue:  "",
			wantSuccess: true,
			wantCmd:     "hget",
			wantBuckets: map[float64]uint64{},
		},
		{
			fieldKey:    "cmdstathis_hget",
			fieldValue:  "fd",
			wantSuccess: false,
			wantCmd:     "hget",
		},
		{
			fieldKey:    "cmdstathist_hget",
			fieldValue:  "fd",
			wantSuccess: false,
			wantCmd:     "hget",
		},
		{
			fieldKey:    "cmdstathist_hget",
			fieldValue:  "fd=malformed",
			wantSuccess: false,
			wantCmd:     "hget",
		},
		{
			fieldKey:    "cmdstathist_get",
			fieldValue:  "10=1191,20=1,50=0,70=0,100=0,150=0,inf=0,sum=,count=1192",
			wantSuccess: false,
			wantCmd:     "get",
			wantBuckets: map[float64]uint64{
				0.00001:     1191,
				0.00002:     1192,
				0.00005:     1192,
				0.00007:     1192,
				0.0001:      1192,
				0.00015:     1192,
				math.Inf(1): 1192,
			},
			wantSum:   0,
			wantCount: 1192,
		},
		{
			fieldKey:    "cmdstathist_get",
			fieldValue:  "10=1191,20=1,50=0,70=0,100=0,150=0,inf=0,sum=aa,count=1192",
			wantSuccess: false,
			wantCmd:     "get",
			wantBuckets: map[float64]uint64{
				0.00001:     1191,
				0.00002:     1192,
				0.00005:     1192,
				0.00007:     1192,
				0.0001:      1192,
				0.00015:     1192,
				math.Inf(1): 1192,
			},
			wantSum:   0,
			wantCount: 1192,
		},
		{
			fieldKey:    "cmdstathist_get",
			fieldValue:  "10=1191,20=1,50=0,70=0,100=0,150=0,inf=0,sum=10000,count=",
			wantSuccess: false,
			wantCmd:     "get",
			wantBuckets: map[float64]uint64{
				0.00001:     1191,
				0.00002:     1192,
				0.00005:     1192,
				0.00007:     1192,
				0.0001:      1192,
				0.00015:     1192,
				math.Inf(1): 1192,
			},
			wantSum:   10000,
			wantCount: 0,
		},
		{
			fieldKey:    "cmdstathist_get",
			fieldValue:  "10=1191,20=1,50=0,70=0,100=0,150=0,inf=0,sum=10000,count=dasd",
			wantSuccess: false,
			wantCmd:     "get",
			wantBuckets: map[float64]uint64{
				0.00001:     1191,
				0.00002:     1192,
				0.00005:     1192,
				0.00007:     1192,
				0.0001:      1192,
				0.00015:     1192,
				math.Inf(1): 1192,
			},
			wantSum:   10000,
			wantCount: 0,
		},
	} {
		t.Run(tst.fieldKey+tst.fieldValue, func(t *testing.T) {
			cmd, count, sum, buckets, err := parseMetricsCommandStatsHist(tst.fieldKey, tst.fieldValue)

			if tst.wantSuccess && err != nil {
				t.Fatalf("err: %s", err)
				return
			}

			if !tst.wantSuccess && err == nil {
				t.Fatalf("expected err!")
				return
			}

			if !tst.wantSuccess {
				return
			}

			if cmd != tst.wantCmd {
				t.Fatalf("cmd not matching, got: %s, wanted: %s", cmd, tst.wantCmd)
			}

			if !reflect.DeepEqual(buckets, tst.wantBuckets) {
				t.Fatalf("cmd not matching, got: %v, wanted: %v", buckets, tst.wantBuckets)
			}

			if count != tst.wantCount {
				t.Fatalf("count not matching, got: %d, wanted: %d", count, tst.wantCount)
			}

			if sum != tst.wantSum {
				t.Fatalf("count not matching, got: %d, wanted: %d", sum, tst.wantSum)
			}
		})
	}

}
