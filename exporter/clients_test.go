package exporter

import (
	"strconv"
	"testing"
	"time"
)

func TestDurationFieldToTimestamp(t *testing.T) {
	nowTs := time.Now().Unix()
	for _, tst := range []struct {
		in          string
		expectedOk  bool
		expectedVal int64
	}{
		{
			in:          "123",
			expectedOk:  true,
			expectedVal: nowTs - 123,
		},
		{
			in:          "0",
			expectedOk:  true,
			expectedVal: nowTs - 0,
		},
		{
			in:         "abc",
			expectedOk: false,
		},
	} {
		res, err := durationFieldToTimestamp(tst.in)
		if err == nil && !tst.expectedOk {
			t.Fatalf("expected not ok, but got no error, input: [%s]", tst.in)
		} else if err != nil && tst.expectedOk {
			t.Fatalf("expected ok, but got error: %s, input: [%s]", err, tst.in)
		}
		if tst.expectedOk {
			resInt64, err := strconv.ParseInt(res, 10, 64)
			if err != nil {
				t.Fatalf("ParseInt( %s ) err: %s", res, err)
			}
			if resInt64 != tst.expectedVal {
				t.Fatalf("expected %d, but got: %d", tst.expectedVal, resInt64)
			}
		}
	}
}

func TestParseClientListString(t *testing.T) {
	convertDurationToTimestampString := func(duration string) string {
		ts, err := durationFieldToTimestamp(duration)
		if err != nil {
			panic(err)
		}
		return ts
	}

	tsts := []struct {
		in           string
		expectedOk   bool
		expectedLbls []string
	}{
		{
			in:           "id=11 addr=127.0.0.1:63508 fd=8 name= age=6321 idle=6320 flags=N db=0 sub=0 psub=0 multi=-1 qbuf=0 qbuf-free=0 obl=0 oll=0 omem=0 events=r cmd=setex",
			expectedOk:   true,
			expectedLbls: []string{"", convertDurationToTimestampString("6321"), convertDurationToTimestampString("6320"), "N", "0", "0", "setex", "127.0.0.1", "63508"},
		}, {
			in:           "id=14 addr=127.0.0.1:64958 fd=9 name=foo age=5 idle=0 flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client",
			expectedOk:   true,
			expectedLbls: []string{"foo", convertDurationToTimestampString("5"), convertDurationToTimestampString("0"), "N", "1", "0", "client", "127.0.0.1", "64958"},
		}, {
			in:         "id=14 addr=127.0.0.1:64958 fd=9 name=foo age=ABCDE idle=0 flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client",
			expectedOk: false,
		}, {
			in:         "id=14 addr=127.0.0.1:64958 fd=9 name=foo age=5 idle=NOPE flags=N db=1 sub=0 psub=0 multi=-1 qbuf=26 qbuf-free=32742 obl=0 oll=0 omem=0 events=r cmd=client",
			expectedOk: false,
		}, {
			in:         "",
			expectedOk: false,
		},
	}

	for _, tst := range tsts {
		lbls, ok := parseClientListString(tst.in)
		if !tst.expectedOk {
			if ok {
				t.Errorf("expected NOT ok, but got ok, input: %s", tst.in)
			}
			continue
		}
		mismatch := false
		for idx, l := range lbls {
			if l != tst.expectedLbls[idx] {
				mismatch = true
				break
			}
		}
		if mismatch {
			t.Errorf("TestParseClientListString( %s ) error. Given: %s Wanted: %s", tst.in, lbls, tst.expectedLbls)
		}
	}
}
