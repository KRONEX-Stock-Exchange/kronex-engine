// Package metrics contains lightweight runtime measurements used during load tests.
package metrics

import (
	"context"
	"log"
	"sync"
	"time"
)

const PublisherLogInterval = 5 * time.Second

// PublisherReporter keeps EventPublisher measurements and emits a compact
// summary periodically. The callbacks keep this package independent of WAL
// and message-queue implementations.
type PublisherReporter struct {
	outputLast func() (uint64, error)
	published  func() uint64

	mu sync.Mutex

	completed uint64
	events    uint64

	walWaitTotal time.Duration
	walWaitMax   time.Duration
	mqTotal      time.Duration
	mqMax        time.Duration
	totalTotal   time.Duration
	totalMax     time.Duration
}

type PublisherRecord struct {
	EventCount int
	WALWait    time.Duration
	MQConfirm  time.Duration
	WALToMQ    time.Duration
}

type publisherSnapshot struct {
	completed    uint64
	events       uint64
	walWaitTotal time.Duration
	walWaitMax   time.Duration
	mqTotal      time.Duration
	mqMax        time.Duration
	totalTotal   time.Duration
	totalMax     time.Duration
}

func NewPublisherReporter(outputLast func() (uint64, error), published func() uint64) *PublisherReporter {
	return &PublisherReporter{outputLast: outputLast, published: published}
}

// Record is called only after a record has completed successfully and its
// publisher cursor has advanced.
func (r *PublisherReporter) Record(record PublisherRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.completed++
	r.events += uint64(record.EventCount)
	r.walWaitTotal += record.WALWait
	r.mqTotal += record.MQConfirm
	r.totalTotal += record.WALToMQ
	if record.WALWait > r.walWaitMax {
		r.walWaitMax = record.WALWait
	}
	if record.MQConfirm > r.mqMax {
		r.mqMax = record.MQConfirm
	}
	if record.WALToMQ > r.totalMax {
		r.totalMax = record.WALToMQ
	}
}

func (r *PublisherReporter) Run(ctx context.Context) {
	ticker := time.NewTicker(PublisherLogInterval)
	defer ticker.Stop()

	last, err := r.outputLast()
	if err != nil {
		log.Printf("event publisher metrics: output last index: %v", err)
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

func (r *PublisherReporter) logWindow(last *uint64) {
	currentLast, err := r.outputLast()
	if err != nil {
		log.Printf("event publisher metrics: output last index: %v", err)
		return
	}
	published := r.published()
	backlog := uint64(0)
	if currentLast > published {
		backlog = currentLast - published
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
	logPublisherWindow(currentLast, published, backlog, produced, s)
}

func (r *PublisherReporter) snapshotAndReset() publisherSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := publisherSnapshot{
		completed: r.completed, events: r.events,
		walWaitTotal: r.walWaitTotal, walWaitMax: r.walWaitMax,
		mqTotal: r.mqTotal, mqMax: r.mqMax,
		totalTotal: r.totalTotal, totalMax: r.totalMax,
	}
	r.completed = 0
	r.events = 0
	r.walWaitTotal = 0
	r.walWaitMax = 0
	r.mqTotal = 0
	r.mqMax = 0
	r.totalTotal = 0
	r.totalMax = 0
	return s
}

func logPublisherWindow(outputLast, published, backlog, produced uint64, s publisherSnapshot) {
	if s.completed == 0 {
		log.Printf("event publisher metrics window=%s output_last=%d event_published=%d backlog=%d produced_rate=%.1f/s drained_rate=0.0/s", PublisherLogInterval, outputLast, published, backlog, float64(produced)/PublisherLogInterval.Seconds())
		return
	}

	count := time.Duration(s.completed)
	log.Printf(
		"event publisher metrics window=%s output_last=%d event_published=%d backlog=%d produced_rate=%.1f/s drained_rate=%.1f/s completed=%d events=%d wal_wait_avg=%s wal_wait_max=%s mq_confirm_avg=%s mq_confirm_max=%s wal_to_mq_avg=%s wal_to_mq_max=%s",
		PublisherLogInterval, outputLast, published, backlog,
		float64(produced)/PublisherLogInterval.Seconds(), float64(s.completed)/PublisherLogInterval.Seconds(),
		s.completed, s.events,
		s.walWaitTotal/count, s.walWaitMax,
		s.mqTotal/count, s.mqMax,
		s.totalTotal/count, s.totalMax,
	)
}
