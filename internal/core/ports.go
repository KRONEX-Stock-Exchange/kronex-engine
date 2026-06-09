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
	Consume(ctx context.Context, queue string, handle func(d Delivery) error) error
}

type Delivery struct {
	Message domain.Message
	Ack     func() error
	Nack    func(requeue bool) error
}
