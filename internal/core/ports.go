package core

import (
	"context"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// 메세지 큐
type Publisher interface {
	Publish(ctx context.Context, msg domain.Message) error
}

type BatchPublisher interface {
	PublishBatch(ctx context.Context, msgs []domain.Message) error
}

type Consumer interface {
	Deliveries(ctx context.Context, queue string) (<-chan Delivery, error)
}

type SnapshotStore interface {
	// 스냅샷 저장 + 최신 keep개만 남기고 정리, 남은 것 중 가장 오래된
	// 스냅샷의 input WAL index(floor)를 반환한다.
	SaveSnapshotAndPrune(ctx context.Context, state []byte, inputWalIndex uint64, keep int) (floor uint64, err error)
	// 스냅샷을 최신 keep개만 남기고 정리(부팅 시 백로그 정리용). 하나도 없으면 hasAny=false.
	PruneSnapshots(ctx context.Context, keep int) (floor uint64, hasAny bool, err error)
	LatestSnapshot(ctx context.Context) (state []byte, inputWalIndex uint64, found bool, err error)
}

type TradeIDStore interface {
	LastTradeID(ctx context.Context) (int64, error)
	LoadMQPublishedCursor(ctx context.Context) (uint64, error)
	LoadDBAppliedCursor(ctx context.Context) (uint64, error)
}

type Delivery struct {
	Queue   string // 수신 큐 (data/admin 플레인 구분)
	Message domain.Message
	Ack     func() error
	Nack    func(requeue bool) error
}
