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

// 신호 유실을 대비해 주기적으로 확인
const backstopInterval = 1 * time.Second

type Store interface {
	Begin(ctx context.Context) (Tx, error)
	LoadMQPublishedCursor(ctx context.Context) (uint64, error)
	SaveMQPublishedCursor(ctx context.Context, index uint64) error
}

type Tx interface {
	SaveTrade(ctx context.Context, trade domain.Trade) error
	UpdateOrderStatus(ctx context.Context, orderID int64, status string, filledQty uint64) error
	RejectOrder(ctx context.Context, orderID int64, reason string) error
	UpdateAccountBalance(ctx context.Context, accountID int32, balance, availableBalance uint64) error
	ActivateAccount(ctx context.Context, accountID int32) error
	UpdateStockStatus(ctx context.Context, stockID int32, status string) error
	UpdateStockPrice(ctx context.Context, stockID int32, price uint64) error
	UpsertHolding(ctx context.Context, holding domain.StockBalance) error
	SaveDBAppliedCursor(ctx context.Context, index uint64) error
	Commit() error
	Rollback() error
}

type Publisher struct {
	output            *wal.WAL
	signal            <-chan struct{} // cap 1 (이미 처리 중일 경우에는 무시)
	cursor            uint64          // 마지막으로 처리 완료한 Output WAL 인덱스
	eventPublishedSeq atomic.Uint64   // event_queue 발행 완료된 마지막 Output WAL 인덱스
	db                Store
	mq                core.Publisher
	eventQueue        string
	metrics           *metrics.PublisherReporter
}

func New(output *wal.WAL, signal <-chan struct{}, db Store, mq core.Publisher, eventQueue string) *Publisher {
	p := &Publisher{
		output:     output,
		signal:     signal,
		db:         db,
		mq:         mq,
		eventQueue: eventQueue,
	}

	p.metrics = metrics.NewPublisherReporter(p.output.LastIndex, p.eventPublishedSeq.Load)
	return p
}

func (p *Publisher) Run(ctx context.Context) error {
	// 영속화된 커서 로드 (재시작 시 어디까지 발행했는지 복원)
	// TODO: 기존의 Cursor가 DB, MQ Cursor로 두가지로 나눠졌기때문에 DB 업데이트 성공 MQ 발행 실패와 같은
	// 상황에서 DB 업데이트를 건너뛰는 로직이 필요함
	cursor, err := p.db.LoadMQPublishedCursor(ctx)
	if err != nil {
		return fmt.Errorf("load MQ published cursor: %w", err)
	}
	p.cursor = cursor
	p.eventPublishedSeq.Store(cursor)

	go p.metrics.Run(ctx)

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

		// DB 저장
		env, dbStarted, dbDuration, err := p.handleRecord(ctx, next, data)
		if err != nil {
			return fmt.Errorf("handle output %d: %w", next, err)
		}

		// 이벤트 전송 및 publisher confirm 대기
		mqStarted := time.Now()
		if err := p.publish(ctx, env); err != nil {
			return fmt.Errorf("publish output %d: %w", next, err)
		}
		mqDuration := time.Since(mqStarted)
		if err := p.db.SaveMQPublishedCursor(ctx, next); err != nil {
			return fmt.Errorf("save MQ published cursor %d: %w", next, err)
		}

		p.cursor = next
		p.eventPublishedSeq.Store(next)

		// Publisher Metrics
		walWait := time.Duration(0)
		if !env.CreatedAt.IsZero() {
			walWait = dbStarted.Sub(env.CreatedAt)
			if walWait < 0 {
				walWait = 0
			}
		}
		walToMQ := time.Since(dbStarted)
		if !env.CreatedAt.IsZero() {
			walToMQ = time.Since(env.CreatedAt)
			if walToMQ < 0 {
				walToMQ = 0
			}
		}
		p.metrics.Record(metrics.PublisherRecord{
			EventCount: len(env.Events),
			WALWait:    walWait,
			DBApply:    dbDuration,
			MQConfirm:  mqDuration,
			WALToMQ:    walToMQ,
		})
	}
	return nil
}

func (p *Publisher) handleRecord(ctx context.Context, index uint64, raw []byte) (core.OutputEnvelope, time.Time, time.Duration, error) {
	var env core.OutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return core.OutputEnvelope{}, time.Time{}, 0, fmt.Errorf("unmarshal output envelope: %w", err)
	}

	dbStarted := time.Now()
	if err := p.applyToDB(ctx, index, env.Events); err != nil {
		return core.OutputEnvelope{}, dbStarted, time.Since(dbStarted), err
	}

	return env, dbStarted, time.Since(dbStarted), nil
}

func (p *Publisher) applyToDB(ctx context.Context, index uint64, events []core.OutputEvent) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for i := range events {
		ev := &events[i]

		switch ev.Pattern {
		case core.PatternTradeExecuted:
			var tr domain.Trade
			if err := json.Unmarshal(ev.Data, &tr); err != nil {
				return fmt.Errorf("unmarshal trade: %w", err)
			}
			err := tx.SaveTrade(ctx, tr)
			if err != nil {
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
			if err := tx.UpdateStockStatus(ctx, st.Id, st.Status.String()); err != nil {
				return err
			}
		case core.PatternStockUpdated:
			var st domain.Stock
			if err := json.Unmarshal(ev.Data, &st); err != nil {
				return fmt.Errorf("unmarshal stock: %w", err)
			}
			if err := tx.UpdateStockPrice(ctx, st.Id, st.Price); err != nil {
				return err
			}
		case core.PatternOrderBookUpdated:
			// 호가 이벤트는 인메모리 원장의 결과를 MQ로만 전달
		default:
			log.Printf("publisher: unknown pattern %q (skip)", ev.Pattern)
		}
	}

	if err := tx.SaveDBAppliedCursor(ctx, index); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (p *Publisher) publish(ctx context.Context, env core.OutputEnvelope) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal output envelope: %w", err)
	}

	return p.mq.Publish(ctx, domain.Message{
		RoutingKey: p.eventQueue,
		Payload:    payload,
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
