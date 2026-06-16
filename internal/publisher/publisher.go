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
	RejectOrder(ctx context.Context, orderID int64, reason string) error
	UpdateAccountBalance(ctx context.Context, accountID int32, balance, availableBalance uint64) error
	ActivateAccount(ctx context.Context, accountID int32) error
	UpdateStockStatus(ctx context.Context, stockID int32, status string) error
	UpsertHolding(ctx context.Context, holding domain.StockBalance) error
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

func (p *Publisher) applyToDB(ctx context.Context, events []core.OutputEvent) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

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
			if err := tx.UpdateOrderStatus(ctx, oe.OrderId, orderStatus(ev.Pattern), oe.FilledQuantity); err != nil {
				return err
			}
		case core.PatternOrderRejected:
			var re domain.OrderRejected
			if err := json.Unmarshal(ev.Data, &re); err != nil {
				return fmt.Errorf("unmarshal order rejected: %w", err)
			}
			if err := tx.RejectOrder(ctx, re.OrderId, re.Reason); err != nil {
				return err
			}
		case core.PatternAccountUpdated:
			var acc domain.Account
			if err := json.Unmarshal(ev.Data, &acc); err != nil {
				return fmt.Errorf("unmarshal account: %w", err)
			}
			if err := tx.UpdateAccountBalance(ctx, acc.Id, acc.Balance, acc.AvailableBalance); err != nil {
				return err
			}
		case core.PatternAccountActivated:
			var acc domain.Account
			if err := json.Unmarshal(ev.Data, &acc); err != nil {
				return fmt.Errorf("unmarshal account: %w", err)
			}
			if err := tx.ActivateAccount(ctx, acc.Id); err != nil {
				return err
			}
		case core.PatternHoldingUpdated:
			var h domain.StockBalance
			if err := json.Unmarshal(ev.Data, &h); err != nil {
				return fmt.Errorf("unmarshal holding: %w", err)
			}
			if err := tx.UpsertHolding(ctx, h); err != nil {
				return err
			}
		case core.PatternStockListed:
			var st domain.Stock
			if err := json.Unmarshal(ev.Data, &st); err != nil {
				return fmt.Errorf("unmarshal stock: %w", err)
			}
			if err := tx.UpdateStockStatus(ctx, st.Id, stockStatus(st.Status)); err != nil {
				return err
			}
		default:
			log.Printf("publisher: unknown pattern %q (skip)", ev.Pattern)
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

func stockStatus(s domain.StockStatus) string {
	switch s {
	case domain.SUSPENDED:
		return "SUSPENDED"
	case domain.DELISTED:
		return "DELISTED"
	default:
		return "LISTED"
	}
}
