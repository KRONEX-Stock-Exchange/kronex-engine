package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/publisher"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/storage/sqlc"
)

var (
	_ publisher.Store = (*EventStore)(nil)
	_ publisher.Tx    = (*eventTx)(nil)
)

const publisherCursorType = sqlc.CursorsTypeEVENT

type EventStore struct {
	db *sql.DB
	q  *sqlc.Queries
}

func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db, q: sqlc.New(db)}
}

func (s *EventStore) Begin(ctx context.Context) (publisher.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	return &eventTx{tx: tx, q: s.q.WithTx(tx)}, nil
}

// 퍼블리셔 커서 로드. 행이 없으면(최초 부팅) 0 반환.
func (s *EventStore) LoadCursor(ctx context.Context) (uint64, error) {
	idx, err := s.q.LoadCursor(ctx, publisherCursorType)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load cursor: %w", err)
	}
	return uint64(idx), nil
}

// 퍼블리셔 커서 저장 (upsert)
func (s *EventStore) SaveCursor(ctx context.Context, index uint64) error {
	if err := s.q.SaveCursor(ctx, sqlc.SaveCursorParams{
		Type:  publisherCursorType,
		Index: int64(index),
	}); err != nil {
		return fmt.Errorf("save cursor %d: %w", index, err)
	}
	return nil
}

type eventTx struct {
	tx *sql.Tx
	q  *sqlc.Queries
}

// 체결 내역 저장
func (t *eventTx) SaveTrade(ctx context.Context, tr domain.Trade) error {
	if err := t.q.SaveTrade(ctx, sqlc.SaveTradeParams{
		ID:           tr.Id,
		StockID:      tr.StockId,
		Price:        tr.Price,
		Quantity:     tr.Quantity,
		MakerOrderID: tr.MakerOrderId,
		TakerOrderID: tr.TakerOrderId,
	}); err != nil {
		return fmt.Errorf("save trade %d: %w", tr.Id, err)
	}
	return nil
}

// 주문 상태/체결수량 갱신
func (t *eventTx) UpdateOrderStatus(ctx context.Context, orderID int64, status string, filledQty uint64) error {
	if err := t.q.UpdateOrderStatus(ctx, sqlc.UpdateOrderStatusParams{
		Status:         sqlc.OrdersStatus(status),
		FilledQuantity: filledQty,
		ID:             orderID,
	}); err != nil {
		return fmt.Errorf("update order %d status: %w", orderID, err)
	}
	return nil
}

func (t *eventTx) Commit() error {
	return t.tx.Commit()
}

func (t *eventTx) Rollback() error {
	if err := t.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("rollback: %w", err)
	}
	return nil
}
