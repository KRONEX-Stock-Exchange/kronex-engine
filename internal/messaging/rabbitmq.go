package messaging

import (
	"context"
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
	conn *amqp.Connection
	ch   *amqp.Channel
}

func Open(_ context.Context, cfg config.RabbitMQ) (*Connection, error) {
	url := fmt.Sprintf("amqp://%s:%s@%s:%d/", cfg.User, cfg.Password, cfg.Host, cfg.Port)

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if cfg.PrefetchCount > 0 {
		if err := ch.Qos(cfg.PrefetchCount, 0, false); err != nil {
			_ = ch.Close()
			_ = conn.Close()
			return nil, fmt.Errorf("set qos: %w", err)
		}
	}

	return &Connection{conn: conn, ch: ch}, nil
}

func (c *Connection) Channel() *amqp.Channel {
	return c.ch
}

// 큐 메세지 발행
func (c *Connection) Publish(ctx context.Context, msg domain.Message) error {
	// NOTE: Exchnage 미사용으로 Default로 고정
	err := c.ch.PublishWithContext(ctx, "", msg.RoutingKey, false, false, amqp.Publishing{
		Body: msg.Payload,
	})
	if err != nil {
		return fmt.Errorf("publish to %q: %w", msg.RoutingKey, err)
	}
	return nil
}

// 큐 메세지 수신
// NOTE: Exchange Default일 경우에는 RabbitMQ가 큐 이름을 Routing Key와 동일하게 지어줌
func (c *Connection) Consume(ctx context.Context, queue string, handle func(core.Delivery) error) error {
	deliveries, err := c.ch.Consume(
		queue, // queue name
		"",    // consumer tag (Auto)
		false, // autoAck
		false, // exclusive
		false, // noLocal
		false, // noWait
		nil,   // args
	)
	if err != nil {
		return fmt.Errorf("consume from %q: %w", queue, err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return nil
			}

			del := core.Delivery{
				Message: domain.Message{
					RoutingKey: d.RoutingKey,
					Payload:    d.Body,
				},
				Ack:  func() error { return d.Ack(false) },
				Nack: func(requeue bool) error { return d.Nack(false, requeue) },
			}
			if err := handle(del); err != nil {
				return err
			}
		}
	}
}

func (c *Connection) Close() error {
	if c.ch != nil {
		if err := c.ch.Close(); err != nil {
			_ = c.conn.Close()
			return fmt.Errorf("close channel: %w", err)
		}
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			return fmt.Errorf("close connection: %w", err)
		}
	}
	return nil
}
