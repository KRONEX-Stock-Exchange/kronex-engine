package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/metrics"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

type EventPublisher struct {
	output            *wal.WAL
	signal            <-chan struct{}
	store             EventPublisherStore
	mq                core.BatchPublisher
	eventQueue        string
	cursor            uint64
	eventPublishedSeq atomic.Uint64
	metrics           *metrics.PublisherReporter
}

const eventPublisherBackstopInterval = time.Second
const eventPublisherBatchSize = 100

func NewEventPublisher(output *wal.WAL, signal <-chan struct{}, store EventPublisherStore, mq core.BatchPublisher, eventQueue string) *EventPublisher {
	p := &EventPublisher{output: output, signal: signal, store: store, mq: mq, eventQueue: eventQueue}
	p.metrics = metrics.NewPublisherReporter(p.output.LastIndex, p.eventPublishedSeq.Load)
	return p
}

func (p *EventPublisher) Run(ctx context.Context) error {
	cursor, err := p.store.LoadMQPublishedCursor(ctx)
	if err != nil {
		return fmt.Errorf("load event published cursor: %w", err)
	}
	p.cursor = cursor
	p.eventPublishedSeq.Store(cursor)
	go p.metrics.Run(ctx)

	backstop := time.NewTicker(eventPublisherBackstopInterval)
	defer backstop.Stop()
	for {
		if err := p.drain(ctx); err != nil {
			log.Printf("event publisher: drain: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.signal:
		case <-backstop.C:
		}
	}
}

func (p *EventPublisher) drain(ctx context.Context) error {
	last, err := p.output.LastIndex()
	if err != nil {
		return fmt.Errorf("output last index: %w", err)
	}
	for p.cursor < last {
		batchEnd := min(p.cursor+eventPublisherBatchSize, last)

		msgs := make([]domain.Message, 0, batchEnd-p.cursor)
		envs := make([]core.OutputEnvelope, 0, batchEnd-p.cursor) // NOTE: 기록용
		for next := p.cursor + 1; next <= batchEnd; next++ {
			raw, err := p.output.Read(next)
			if err != nil {
				return fmt.Errorf("read output %d: %w", next, err)
			}
			var env core.OutputEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("unmarshal output envelope %d: %w", next, err)
			}

			payload, err := json.Marshal(env)
			if err != nil {
				return fmt.Errorf("marshal output envelope %d: %w", next, err)
			}

			msgs = append(msgs, domain.Message{RoutingKey: p.eventQueue, Payload: payload})
			envs = append(envs, env)
		}

		started := time.Now()
		if err := p.mq.PublishBatch(ctx, msgs); err != nil {
			return fmt.Errorf("publish batch output %d-%d: %w", p.cursor+1, batchEnd, err)
		}
		mqConfirm := time.Since(started)
		if err := p.store.SaveMQPublishedCursor(ctx, batchEnd); err != nil {
			return fmt.Errorf("save event published cursor %d: %w", batchEnd, err)
		}

		p.cursor = batchEnd
		p.eventPublishedSeq.Store(batchEnd)

		p.recordBatch(envs, started, mqConfirm)
	}

	return nil
}

// 배치 내 가장 오래된(첫) envelope 기준으로 지표를 기록한다 (가장 오래 기다린 항목이 tail latency를 대표함)
func (p *EventPublisher) recordBatch(envs []core.OutputEnvelope, started time.Time, mqConfirm time.Duration) {
	events := 0
	for _, env := range envs {
		events += len(env.Events)
	}

	walWait := time.Duration(0)
	walToMQ := time.Duration(0)
	if len(envs) > 0 && !envs[0].CreatedAt.IsZero() {
		walWait = max(started.Sub(envs[0].CreatedAt), 0)
		walToMQ = max(time.Since(envs[0].CreatedAt), 0)
	}

	p.metrics.Record(metrics.PublisherRecord{
		EventCount: events,
		WALWait:    walWait,
		MQConfirm:  mqConfirm,
		WALToMQ:    walToMQ,
	})
}
