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
const dbProjectorBatchSize = 100

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
		batchEnd := min(p.cursor+dbProjectorBatchSize, last)

		envs := make([]core.OutputEnvelope, 0, batchEnd-p.cursor)
		for next := p.cursor + 1; next <= batchEnd; next++ {
			raw, err := p.output.Read(next)
			if err != nil {
				return fmt.Errorf("read output %d: %w", next, err)
			}
			var env core.OutputEnvelope
			if err := json.Unmarshal(raw, &env); err != nil {
				return fmt.Errorf("unmarshal output envelope %d: %w", next, err)
			}
			envs = append(envs, env)
		}

		started := time.Now()
		if err := p.applyBatch(ctx, batchEnd, envs); err != nil {
			return fmt.Errorf("apply batch output %d-%d: %w", p.cursor+1, batchEnd, err)
		}
		dbApply := time.Since(started)
		p.cursor = batchEnd
		p.appliedSeq.Store(batchEnd)

		p.recordBatch(envs, started, dbApply)
	}
	return nil
}

// envs 전체를 트랜잭션 하나로 묶어 적용하고, 커서도 배치당 1번만 저장한다
func (p *DBProjector) applyBatch(ctx context.Context, index uint64, envs []core.OutputEnvelope) error {
	tx, err := p.store.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, env := range envs {
		for i := range env.Events {
			if err := applyEvent(ctx, tx, env.Events[i]); err != nil {
				return err
			}
		}
	}
	if err := tx.SaveDBAppliedCursor(ctx, index); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// 배치 내 가장 오래된(첫) envelope 기준으로 지표를 기록한다 (가장 오래 기다린 항목이 tail latency를 대표함)
func (p *DBProjector) recordBatch(envs []core.OutputEnvelope, started time.Time, dbApply time.Duration) {
	events := 0
	for _, env := range envs {
		events += len(env.Events)
	}

	walWait := time.Duration(0)
	if len(envs) > 0 && !envs[0].CreatedAt.IsZero() {
		walWait = max(started.Sub(envs[0].CreatedAt), 0)
	}

	p.metrics.Record(metrics.DBProjectorRecord{EventCount: events, WALWait: walWait, DBApply: dbApply})
}

func applyEvent(ctx context.Context, tx Tx, ev core.OutputEvent) error {
	switch ev.Pattern {
	case core.PatternTradeExecuted:
		var trade domain.Trade
		if err := json.Unmarshal(ev.Data, &trade); err != nil {
			return fmt.Errorf("unmarshal trade: %w", err)
		}
		return tx.SaveTrade(ctx, trade)
	case core.PatternOrderOpen, core.PatternOrderFilled, core.PatternOrderCanceled, core.PatternOrderReplaced, core.PatternOrderCompleted:
		var order domain.OrderEvent
		if err := json.Unmarshal(ev.Data, &order); err != nil {
			return fmt.Errorf("unmarshal order event: %w", err)
		}
		return tx.UpdateOrderState(ctx, order.Id, orderStatus(ev.Pattern), order.Quantity, order.FilledQuantity)
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
	case core.PatternOrderReplaced:
		return "REPLACED"
	case core.PatternOrderCompleted:
		return "COMPLETED"
	default:
		return "OPEN"
	}
}
