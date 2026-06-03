package messaging

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/config"
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

	return &Connection{conn: conn, ch: ch}, nil
}

func (c *Connection) Channel() *amqp.Channel {
	return c.ch
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
