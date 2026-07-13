package metrics

import (
	"context"
	"log"
	"sync"
	"time"
)

// DBProjectorReporter keeps DB projection measurements separate from event publication.
type DBProjectorReporter struct {
	outputLast func() (uint64, error)
	applied    func() uint64

	mu sync.Mutex

	completed uint64
	events    uint64
	waitTotal time.Duration
	waitMax   time.Duration
	dbTotal   time.Duration
	dbMax     time.Duration
}

type DBProjectorRecord struct {
	EventCount int
	WALWait    time.Duration
	DBApply    time.Duration
}

type dbProjectorSnapshot struct {
	completed uint64
	events    uint64
	waitTotal time.Duration
	waitMax   time.Duration
	dbTotal   time.Duration
	dbMax     time.Duration
}

func NewDBProjectorReporter(outputLast func() (uint64, error), applied func() uint64) *DBProjectorReporter {
	return &DBProjectorReporter{outputLast: outputLast, applied: applied}
}

func (r *DBProjectorReporter) Record(record DBProjectorRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed++
	r.events += uint64(record.EventCount)
	r.waitTotal += record.WALWait
	r.dbTotal += record.DBApply
	if record.WALWait > r.waitMax {
		r.waitMax = record.WALWait
	}
	if record.DBApply > r.dbMax {
		r.dbMax = record.DBApply
	}
}

func (r *DBProjectorReporter) Run(ctx context.Context) {
	ticker := time.NewTicker(PublisherLogInterval)
	defer ticker.Stop()
	last, err := r.outputLast()
	if err != nil {
		log.Printf("DB projector metrics: output last index: %v", err)
		last = 0
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.logWindow(&last)
		}
	}
}

func (r *DBProjectorReporter) logWindow(last *uint64) {
	currentLast, err := r.outputLast()
	if err != nil {
		log.Printf("DB projector metrics: output last index: %v", err)
		return
	}
	applied := r.applied()
	backlog := uint64(0)
	if currentLast > applied {
		backlog = currentLast - applied
	}
	produced := uint64(0)
	if currentLast >= *last {
		produced = currentLast - *last
	}
	*last = currentLast

	s := r.snapshotAndReset()
	if produced == 0 && s.completed == 0 && backlog == 0 {
		return
	}
	if s.completed == 0 {
		log.Printf("DB projector metrics window=%s output_last=%d db_applied=%d backlog=%d produced_rate=%.1f/s drained_rate=0.0/s", PublisherLogInterval, currentLast, applied, backlog, float64(produced)/PublisherLogInterval.Seconds())
		return
	}
	count := time.Duration(s.completed)
	log.Printf(
		"DB projector metrics window=%s output_last=%d db_applied=%d backlog=%d produced_rate=%.1f/s drained_rate=%.1f/s completed=%d events=%d wal_wait_avg=%s wal_wait_max=%s db_apply_avg=%s db_apply_max=%s",
		PublisherLogInterval, currentLast, applied, backlog,
		float64(produced)/PublisherLogInterval.Seconds(), float64(s.completed)/PublisherLogInterval.Seconds(),
		s.completed, s.events,
		s.waitTotal/count, s.waitMax,
		s.dbTotal/count, s.dbMax,
	)
}

func (r *DBProjectorReporter) snapshotAndReset() dbProjectorSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := dbProjectorSnapshot{completed: r.completed, events: r.events, waitTotal: r.waitTotal, waitMax: r.waitMax, dbTotal: r.dbTotal, dbMax: r.dbMax}
	r.completed = 0
	r.events = 0
	r.waitTotal = 0
	r.waitMax = 0
	r.dbTotal = 0
	r.dbMax = 0
	return s
}
