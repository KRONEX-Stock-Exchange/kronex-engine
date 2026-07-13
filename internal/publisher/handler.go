package publisher

import (
	"context"
	"log"
	"sync"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

type Handler struct {
	output     *wal.WAL
	signal     <-chan struct{}
	db         DBProjectorStore
	events     EventPublisherStore
	mq         core.Publisher
	eventQueue string
}

func NewHandler(output *wal.WAL, signal <-chan struct{}, db DBProjectorStore, events EventPublisherStore, mq core.Publisher, eventQueue string) *Handler {
	return &Handler{
		output:     output,
		signal:     signal,
		db:         db,
		events:     events,
		mq:         mq,
		eventQueue: eventQueue,
	}
}

func (h *Handler) Run(ctx context.Context) error {
	dbSignal := make(chan struct{}, 1)
	eventSignal := make(chan struct{}, 1)
	dbProjector := NewDBProjector(h.output, dbSignal, h.db)
	eventPublisher := NewEventPublisher(h.output, eventSignal, h.events, h.mq, h.eventQueue)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		h.fanOutSignals(ctx, dbSignal, eventSignal)
	}()
	go func() {
		defer wg.Done()
		if err := dbProjector.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("DB projector stopped: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := eventPublisher.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("event publisher stopped: %v", err)
		}
	}()

	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

func (h *Handler) fanOutSignals(ctx context.Context, dbSignal, eventSignal chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.signal:
			notify(dbSignal)
			notify(eventSignal)
		}
	}
}

func notify(signal chan<- struct{}) {
	select {
	case signal <- struct{}{}:
	default:
	}
}
