package core

import (
	"context"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// 메세지 큐
type Publisher interface {
	Publish(ctx context.Context, msg domain.Message) error
}

type Consumer interface {
	Deliveries(ctx context.Context, queue string) (<-chan Delivery, error)
}

type SnapshotStore interface {
	SaveSnapshot(ctx context.Context, state []byte, inputWalIndex uint64) error
	LatestSnapshot(ctx context.Context) (state []byte, inputWalIndex uint64, found bool, err error)
}

type Delivery struct {
	Queue   string // 수신 큐 (data/admin 플레인 구분)
	Message domain.Message
	Ack     func() error
	Nack    func(requeue bool) error
}
