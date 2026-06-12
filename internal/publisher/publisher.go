package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

// 신호 유실을 대비해 주기적으로 확인
const backstopInterval = 1 * time.Second

type Store interface {
	Begin(ctx context.Context) (Tx, error)
	LoadCursor(ctx context.Context) (uint64, error)
	SaveCursor(ctx context.Context, index uint64) error
}

type Tx interface {
	SaveTrade(ctx context.Context, trade domain.Trade) error
	UpdateOrderStatus(ctx context.Context, orderID int64, status string, filledQty uint64) error
	Commit() error
	Rollback() error
}

type Publisher struct {
	output     *wal.WAL
	signal     <-chan struct{} // cap 1 (이미 처리 중일 경우에는 무시)
	cursor     uint64          // 마지막으로 처리 완료한 Output WAL 인덱스
	db         Store
	mq         core.Publisher
	eventQueue string
}

func New(output *wal.WAL, signal <-chan struct{}, db Store, mq core.Publisher, eventQueue string) *Publisher {
	return &Publisher{
		output:     output,
		signal:     signal,
		db:         db,
		mq:         mq,
		eventQueue: eventQueue,
	}
}

func (p *Publisher) Run(ctx context.Context) error {
	// 영속화된 커서 로드 (재시작 시 어디까지 발행했는지 복원)
	cursor, err := p.db.LoadCursor(ctx)
	if err != nil {
		return fmt.Errorf("load cursor: %w", err)
	}
	p.cursor = cursor

	backstop := time.NewTicker(backstopInterval)
	defer backstop.Stop()

	for {
		// Output WAL 처리
		if err := p.drain(ctx); err != nil {
			log.Printf("publisher: drain: %v", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.signal:
		case <-backstop.C:
		}
	}
}

// Cursor + 1 부터 가장 최근 Output WAL까지 처리
func (p *Publisher) drain(ctx context.Context) error {
	last, err := p.output.LastIndex()
	if err != nil {
		return fmt.Errorf("output last index: %w", err)
	}

	for p.cursor < last {
		next := p.cursor + 1
		data, err := p.output.Read(next)
		if err != nil {
			return fmt.Errorf("read output %d: %w", next, err)
		}

		// DB 저장 및 이벤트 전송
		if err := p.handleRecord(ctx, data); err != nil {
			return fmt.Errorf("handle output %d: %w", next, err)
		}

		// 커서 저장
		if err := p.db.SaveCursor(ctx, next); err != nil {
			return fmt.Errorf("save cursor %d: %w", next, err)
		}
		p.cursor = next
	}
	return nil
}

func (p *Publisher) handleRecord(ctx context.Context, raw []byte) error {
	var env core.OutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("unmarshal output envelope: %w", err)
	}

	// DB 저장
	if err := p.applyToDB(ctx, env.Events); err != nil {
		return err
	}

	// 이벤트 전송
	return p.publish(ctx, raw)
}

type orderUpdate struct {
	status    string
	filledQty uint64
}

func (p *Publisher) applyToDB(ctx context.Context, events []core.OutputEvent) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 주문별 마지막 상태만 DB 반영
	orderUpdates := make(map[int64]orderUpdate)
	var orderIDs []int64

	for _, ev := range events {
		switch ev.Pattern {
		case core.PatternTradeExecuted:
			var tr domain.Trade
			if err := json.Unmarshal(ev.Data, &tr); err != nil {
				return fmt.Errorf("unmarshal trade: %w", err)
			}
			if err := tx.SaveTrade(ctx, tr); err != nil {
				return err
			}
		case core.PatternOrderOpen, core.PatternOrderFilled, core.PatternOrderCanceled:
			var oe domain.OrderEvent
			if err := json.Unmarshal(ev.Data, &oe); err != nil {
				return fmt.Errorf("unmarshal order event: %w", err)
			}
			if _, seen := orderUpdates[oe.OrderId]; !seen {
				orderIDs = append(orderIDs, oe.OrderId)
			}
			orderUpdates[oe.OrderId] = orderUpdate{orderStatus(ev.Pattern), oe.FilledQuantity}
		default:
			log.Printf("publisher: unknown pattern %q (skip)", ev.Pattern)
		}
	}

	// 주문별 마지막 상태만 반영
	for _, id := range orderIDs {
		u := orderUpdates[id]
		if err := tx.UpdateOrderStatus(ctx, id, u.status, u.filledQty); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (p *Publisher) publish(ctx context.Context, raw []byte) error {
	return p.mq.Publish(ctx, domain.Message{
		RoutingKey: p.eventQueue,
		Payload:    raw,
	})
}

func orderStatus(pattern string) string {
	switch pattern {
	case core.PatternOrderFilled:
		return "FILLED"
	case core.PatternOrderCanceled:
		return "CANCELED"
	default:
		return "OPEN"
	}
}
