package publisher

import (
	"context"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

type DBProjectorStore interface {
	Begin(ctx context.Context) (Tx, error)
	LoadDBAppliedCursor(ctx context.Context) (uint64, error)
}

type EventPublisherStore interface {
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
	DeleteHolding(ctx context.Context, accountID, stockID int32) error
	SaveDBAppliedCursor(ctx context.Context, index uint64) error
	Commit() error
	Rollback() error
}
