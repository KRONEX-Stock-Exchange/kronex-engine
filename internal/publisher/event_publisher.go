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
	mq                core.Publisher
	eventQueue        string
	cursor            uint64
	eventPublishedSeq atomic.Uint64
	metrics           *metrics.PublisherReporter
}

const eventPublisherBackstopInterval = time.Second

func NewEventPublisher(output *wal.WAL, signal <-chan struct{}, store EventPublisherStore, mq core.Publisher, eventQueue string) *EventPublisher {
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
		next := p.cursor + 1
		raw, err := p.output.Read(next)
		if err != nil {
			return fmt.Errorf("read output %d: %w", next, err)
		}
		var env core.OutputEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("unmarshal output envelope %d: %w", next, err)
		}

		started := time.Now()
		if err := p.publish(ctx, env); err != nil {
			return fmt.Errorf("publish output %d: %w", next, err)
		}
		mqConfirm := time.Since(started)
		if err := p.store.SaveMQPublishedCursor(ctx, next); err != nil {
			return fmt.Errorf("save event published cursor %d: %w", next, err)
		}
		p.cursor = next
		p.eventPublishedSeq.Store(next)

		walWait := time.Duration(0)
		walToMQ := time.Since(started)
		if !env.CreatedAt.IsZero() {
			walWait = started.Sub(env.CreatedAt)
			if walWait < 0 {
				walWait = 0
			}
			walToMQ = time.Since(env.CreatedAt)
			if walToMQ < 0 {
				walToMQ = 0
			}
		}
		p.metrics.Record(metrics.PublisherRecord{
			EventCount: len(env.Events),
			WALWait:    walWait,
			MQConfirm:  mqConfirm,
			WALToMQ:    walToMQ,
		})
	}
	return nil
}

func (p *EventPublisher) publish(ctx context.Context, env core.OutputEnvelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal output envelope: %w", err)
	}
	return p.mq.Publish(ctx, domain.Message{RoutingKey: p.eventQueue, Payload: payload})
}
