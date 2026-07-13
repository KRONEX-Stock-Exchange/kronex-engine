package metrics

import (
	"testing"
	"time"
)

func TestPublisherReporterSnapshotAndResetKeepsMutexUsable(t *testing.T) {
	reporter := NewPublisherReporter(func() (uint64, error) { return 0, nil }, func() uint64 { return 0 })
	reporter.Record(PublisherRecord{
		EventCount: 3,
		WALWait:    time.Second,
		DBApply:    2 * time.Second,
		MQConfirm:  3 * time.Second,
		WALToMQ:    6 * time.Second,
	})

	first := reporter.snapshotAndReset()
	if first.completed != 1 || first.events != 3 {
		t.Fatalf("first snapshot = %+v, want one completed envelope with three events", first)
	}

	second := reporter.snapshotAndReset()
	if second.completed != 0 || second.events != 0 {
		t.Fatalf("second snapshot = %+v, want zeroed metrics", second)
	}
}
