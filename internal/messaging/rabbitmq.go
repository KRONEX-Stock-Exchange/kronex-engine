package messaging

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/config"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

var (
	_ core.Publisher = (*Connection)(nil)
	_ core.Consumer  = (*Connection)(nil)
)

type Connection struct {
	conn      *amqp.Connection
	consumeCh *amqp.Channel // 주문 수신 (Qos prefetch)
	publishCh *amqp.Channel // 이벤트 발행 (publisher confirm)
}

func Open(_ context.Context, cfg config.RabbitMQ) (*Connection, error) {
	url := fmt.Sprintf("amqp://%s:%s@%s:%d/", cfg.User, cfg.Password, cfg.Host, cfg.Port)

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	// consume ch
	consumeCh, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open consume channel: %w", err)
	}
	if cfg.PrefetchCount > 0 {
		if err := consumeCh.Qos(cfg.PrefetchCount, 0, false); err != nil {
			_ = consumeCh.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("set qos: %w", err)
		}
	}

	// publish ch
	publishCh, err := conn.Channel()
	if err != nil {
		_ = consumeCh.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("open publish channel: %w", err)
	}
	if err := publishCh.Confirm(false); err != nil {
		_ = publishCh.Close()
		_ = consumeCh.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("set confirm mode: %w", err)
	}

	return &Connection{conn: conn, consumeCh: consumeCh, publishCh: publishCh}, nil
}

func (c *Connection) Publish(ctx context.Context, msg domain.Message) error {
	// NOTE: Exchnage 미사용으로 Default로 고정
	conf, err := c.publishCh.PublishWithDeferredConfirmWithContext(ctx, "", msg.RoutingKey, false, false, amqp.Publishing{
		Body: msg.Payload,
	})
	if err != nil {
		return fmt.Errorf("publish to %q: %w", msg.RoutingKey, err)
	}

	acked, err := conf.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("await confirm %q: %w", msg.RoutingKey, err)
	}
	if !acked {
		return fmt.Errorf("publish to %q nacked by broker", msg.RoutingKey)
	}

	return nil
}

// 큐 메세지 수신 채널 반환
// NOTE: Exchange Default일 경우에는 RabbitMQ가 큐 이름을 Routing Key와 동일하게 지어줌
func (c *Connection) Deliveries(ctx context.Context, queue string) (<-chan core.Delivery, error) {
	amqpDeliveries, err := c.consumeCh.Consume(
		queue, // queue name
		"",    // consumer tag (Auto)
		false, // autoAck
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,   // args
	)
	if err != nil {
		return nil, fmt.Errorf("consume from %q: %w", queue, err)
	}

	out := make(chan core.Delivery)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-amqpDeliveries:
				if !ok {
					return
				}
				del := core.Delivery{
					Message: domain.Message{
						RoutingKey: d.RoutingKey,
						Payload:    d.Body,
					},
					Ack:  func() error { return d.Ack(false) },
					Nack: func(requeue bool) error { return d.Nack(false, requeue) },
				}
				select {
				case out <- del:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (c *Connection) Close() error {
	var errs []error
	if c.publishCh != nil {
		if err := c.publishCh.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close publish channel: %w", err))
		}
	}
	if c.consumeCh != nil {
		if err := c.consumeCh.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close consume channel: %w", err))
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close connection: %w", err))
		}
	}
	return errors.Join(errs...)
}
