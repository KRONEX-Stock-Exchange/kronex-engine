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

type EventStore struct {
	db *sql.DB
	q  *sqlc.Queries
}

func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db, q: sqlc.New(db)}
}

func (s *EventStore) LastTradeID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM trades").Scan(&id); err != nil {
		return 0, fmt.Errorf("load last trade id: %w", err)
	}
	return id, nil
}

func (s *EventStore) Begin(ctx context.Context) (publisher.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	return &eventTx{tx: tx, q: s.q.WithTx(tx)}, nil
}

// MQ 발행 커서 로드. 행이 없으면(최초 부팅) 0 반환.
func (s *EventStore) LoadMQPublishedCursor(ctx context.Context) (uint64, error) {
	idx, err := s.q.LoadMQPublishedCursor(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("load MQ published cursor: %w", err)
	}
	return uint64(idx), nil
}

// MQ 발행 커서 저장 (upsert)
func (s *EventStore) SaveMQPublishedCursor(ctx context.Context, index uint64) error {
	if err := s.q.SaveMQPublishedCursor(ctx, int64(index)); err != nil {
		return fmt.Errorf("save MQ published cursor %d: %w", index, err)
	}
	return nil
}

type eventTx struct {
	tx *sql.Tx
	q  *sqlc.Queries
}

// DB 변경과 같은 트랜잭션에서 마지막으로 반영한 Output WAL 인덱스를 저장한다.
func (t *eventTx) SaveDBAppliedCursor(ctx context.Context, index uint64) error {
	if err := t.q.SaveDBAppliedCursor(ctx, int64(index)); err != nil {
		return fmt.Errorf("save DB applied cursor %d: %w", index, err)
	}
	return nil
}

// 체결 내역 저장
func (t *eventTx) SaveTrade(ctx context.Context, tr domain.Trade) error {
	err := t.q.SaveTrade(ctx, sqlc.SaveTradeParams{
		ID:           tr.Id,
		StockID:      tr.StockId,
		Price:        tr.Price,
		Quantity:     tr.Quantity,
		MakerOrderID: tr.MakerOrderId,
		TakerOrderID: tr.TakerOrderId,
		MatchedAt:    tr.ExecutedAt,
	})
	if err != nil {
		return fmt.Errorf("save trade: %w", err)
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

// 주문 거부 처리
func (t *eventTx) RejectOrder(ctx context.Context, orderID int64, reason string) error {
	if err := t.q.RejectOrder(ctx, sqlc.RejectOrderParams{
		RejectReason: sqlc.NullOrdersRejectReason{
			OrdersRejectReason: sqlc.OrdersRejectReason(reason),
			Valid:              true,
		},
		ID: orderID,
	}); err != nil {
		return fmt.Errorf("reject order %d: %w", orderID, err)
	}
	return nil
}

// 계좌 잔고/가용잔고 갱신
func (t *eventTx) UpdateAccountBalance(ctx context.Context, accountID int32, balance, availableBalance uint64) error {
	if err := t.q.UpdateAccountBalance(ctx, sqlc.UpdateAccountBalanceParams{
		Balance:          balance,
		AvailableBalance: availableBalance,
		ID:               accountID,
	}); err != nil {
		return fmt.Errorf("update account %d balance: %w", accountID, err)
	}
	return nil
}

// 계좌 활성화 (status = ACTIVE)
func (t *eventTx) ActivateAccount(ctx context.Context, accountID int32) error {
	if err := t.q.ActivateAccount(ctx, accountID); err != nil {
		return fmt.Errorf("activate account %d: %w", accountID, err)
	}
	return nil
}

// 종목 현재가 갱신
func (t *eventTx) UpdateStockPrice(ctx context.Context, stockID int32, price uint64) error {
	if err := t.q.UpdateStockPrice(ctx, sqlc.UpdateStockPriceParams{
		Price: price,
		ID:    stockID,
	}); err != nil {
		return fmt.Errorf("update stock %d price: %w", stockID, err)
	}
	return nil
}

// 종목 상태 갱신 (상장/상폐/거래정지)
func (t *eventTx) UpdateStockStatus(ctx context.Context, stockID int32, status string) error {
	if err := t.q.UpdateStockStatus(ctx, sqlc.UpdateStockStatusParams{
		Status: sqlc.StocksStatus(status),
		ID:     stockID,
	}); err != nil {
		return fmt.Errorf("update stock %d status: %w", stockID, err)
	}
	return nil
}

// 보유종목 갱신 (없으면 생성)
func (t *eventTx) UpsertHolding(ctx context.Context, h domain.StockBalance) error {
	if err := t.q.UpsertHolding(ctx, sqlc.UpsertHoldingParams{
		AccountID:         h.AccountId,
		StockID:           h.StockId,
		Quantity:          h.Quantity,
		AvailableQuantity: h.AvailableQuantity,
		Average:           h.Average,
		TotalBuyAmount:    h.TotalBuyAmount,
	}); err != nil {
		return fmt.Errorf("upsert holding (account=%d stock=%d): %w", h.AccountId, h.StockId, err)
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
