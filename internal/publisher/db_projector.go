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

const dbProjectorBackstopInterval = time.Second

type DBProjector struct {
	output     *wal.WAL
	signal     <-chan struct{}
	store      DBProjectorStore
	cursor     uint64
	appliedSeq atomic.Uint64
	metrics    *metrics.DBProjectorReporter
}

func NewDBProjector(output *wal.WAL, signal <-chan struct{}, store DBProjectorStore) *DBProjector {
	p := &DBProjector{output: output, signal: signal, store: store}
	p.metrics = metrics.NewDBProjectorReporter(p.output.LastIndex, p.appliedSeq.Load)
	return p
}

func (p *DBProjector) Run(ctx context.Context) error {
	cursor, err := p.store.LoadDBAppliedCursor(ctx)
	if err != nil {
		return fmt.Errorf("load DB applied cursor: %w", err)
	}
	p.cursor = cursor
	p.appliedSeq.Store(cursor)
	go p.metrics.Run(ctx)

	backstop := time.NewTicker(dbProjectorBackstopInterval)
	defer backstop.Stop()
	for {
		if err := p.drain(ctx); err != nil {
			log.Printf("DB projector: drain: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.signal:
		case <-backstop.C:
		}
	}
}

func (p *DBProjector) drain(ctx context.Context) error {
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
		env, started, dbApply, err := p.apply(ctx, next, raw)
		if err != nil {
			return fmt.Errorf("apply output %d: %w", next, err)
		}
		p.cursor = next
		p.appliedSeq.Store(next)
		walWait := time.Duration(0)
		if !env.CreatedAt.IsZero() {
			walWait = started.Sub(env.CreatedAt)
			if walWait < 0 {
				walWait = 0
			}
		}
		p.metrics.Record(metrics.DBProjectorRecord{EventCount: len(env.Events), WALWait: walWait, DBApply: dbApply})
	}
	return nil
}

func (p *DBProjector) apply(ctx context.Context, index uint64, raw []byte) (core.OutputEnvelope, time.Time, time.Duration, error) {
	var env core.OutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return core.OutputEnvelope{}, time.Time{}, 0, fmt.Errorf("unmarshal output envelope: %w", err)
	}

	started := time.Now()
	tx, err := p.store.Begin(ctx)
	if err != nil {
		return core.OutputEnvelope{}, started, time.Since(started), fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	for i := range env.Events {
		if err := applyEvent(ctx, tx, env.Events[i]); err != nil {
			return core.OutputEnvelope{}, started, time.Since(started), err
		}
	}
	if err := tx.SaveDBAppliedCursor(ctx, index); err != nil {
		return core.OutputEnvelope{}, started, time.Since(started), err
	}
	if err := tx.Commit(); err != nil {
		return core.OutputEnvelope{}, started, time.Since(started), fmt.Errorf("commit transaction: %w", err)
	}
	return env, started, time.Since(started), nil
}

func applyEvent(ctx context.Context, tx Tx, ev core.OutputEvent) error {
	switch ev.Pattern {
	case core.PatternTradeExecuted:
		var trade domain.Trade
		if err := json.Unmarshal(ev.Data, &trade); err != nil {
			return fmt.Errorf("unmarshal trade: %w", err)
		}
		return tx.SaveTrade(ctx, trade)
	case core.PatternOrderOpen, core.PatternOrderFilled, core.PatternOrderCanceled, core.PatternOrderCompleted:
		var order domain.OrderEvent
		if err := json.Unmarshal(ev.Data, &order); err != nil {
			return fmt.Errorf("unmarshal order event: %w", err)
		}
		return tx.UpdateOrderStatus(ctx, order.OrderId, orderStatus(ev.Pattern), order.FilledQuantity)
	case core.PatternOrderRejected:
		var rejected domain.OrderRejected
		if err := json.Unmarshal(ev.Data, &rejected); err != nil {
			return fmt.Errorf("unmarshal order rejected: %w", err)
		}
		return tx.RejectOrder(ctx, rejected.OrderId, rejected.Reason)
	case core.PatternAccountUpdated:
		var account domain.Account
		if err := json.Unmarshal(ev.Data, &account); err != nil {
			return fmt.Errorf("unmarshal account: %w", err)
		}
		return tx.UpdateAccountBalance(ctx, account.Id, account.Balance, account.AvailableBalance)
	case core.PatternAccountActivated:
		var account domain.Account
		if err := json.Unmarshal(ev.Data, &account); err != nil {
			return fmt.Errorf("unmarshal account: %w", err)
		}
		return tx.ActivateAccount(ctx, account.Id)
	case core.PatternHoldingUpdated:
		var holding domain.StockBalance
		if err := json.Unmarshal(ev.Data, &holding); err != nil {
			return fmt.Errorf("unmarshal holding: %w", err)
		}
		if holding.Quantity == 0 && holding.AvailableQuantity == 0 {
			return tx.DeleteHolding(ctx, holding.AccountId, holding.StockId)
		}

		return tx.UpsertHolding(ctx, holding)
	case core.PatternStockListed:
		var stock domain.Stock
		if err := json.Unmarshal(ev.Data, &stock); err != nil {
			return fmt.Errorf("unmarshal stock: %w", err)
		}
		return tx.UpdateStockStatus(ctx, stock.Id, stock.Status.String())
	case core.PatternStockUpdated:
		var stock domain.Stock
		if err := json.Unmarshal(ev.Data, &stock); err != nil {
			return fmt.Errorf("unmarshal stock: %w", err)
		}
		return tx.UpdateStockPrice(ctx, stock.Id, stock.Price)
	case core.PatternOrderBookUpdated:
		return nil
	default:
		log.Printf("DB projector: unknown pattern %q (skip)", ev.Pattern)
		return nil
	}
}

func orderStatus(pattern string) string {
	switch pattern {
	case core.PatternOrderFilled:
		return "FILLED"
	case core.PatternOrderCanceled:
		return "CANCELED"
	case core.PatternOrderCompleted:
		return "COMPLETED"
	default:
		return "OPEN"
	}
}
