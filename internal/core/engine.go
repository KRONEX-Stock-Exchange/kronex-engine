package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

const (
	PatternOrderCreated = "order.created"
)

type Engine struct {
	con    Consumer
	queue  string
	input  *wal.WAL
	output *wal.WAL
}

func NewEngine(con Consumer, queue string) (*Engine, error) {
	input, err := wal.Open("./data/wal/input", nil)
	if err != nil {
		return nil, fmt.Errorf("open input wal: %w", err)
	}

	output, err := wal.Open("./data/wal/output", nil)
	if err != nil {
		return nil, fmt.Errorf("open output wal: %w", err)
	}

	return &Engine{con: con, queue: queue, input: input, output: output}, nil
}

func (e *Engine) Close() error {
	return errors.Join(e.input.Close(), e.output.Close())
}

func (e *Engine) Run(ctx context.Context) error {
	return e.con.Consume(ctx, e.queue, e.handle)
}

type envelope struct {
	Pattern string          `json:"pattern"`
	Data    json.RawMessage `json:"data"`
}

func (e *Engine) handle(d Delivery) error {
	var env envelope
	if err := json.Unmarshal(d.Message.Payload, &env); err != nil {
		log.Printf("engine: decode envelope: %v", err)
		return d.Nack(false)
	}

	// Input WAL 작성
	if _, err := e.input.Append(d.Message.Payload); err != nil {
		log.Printf("engine: append input wal: %v", err)
		return d.Nack(true)
	}

	switch env.Pattern {
	case PatternOrderCreated:
		return e.handleOrder(d, env.Data)
	default:
		log.Printf("engine: unknown pattern %q", env.Pattern)
		return d.Nack(false)
	}
}
