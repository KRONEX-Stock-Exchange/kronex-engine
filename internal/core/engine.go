package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

const (
	PatternOrderCreated  = "order.created"
	PatternTradeExecuted = "trade.executed"
)

type Engine struct {
	con    Consumer
	queue  string
	input  *wal.WAL
	output *wal.WAL
	state  *ledger.State
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

	return &Engine{
		con:    con,
		queue:  queue,
		input:  input,
		output: output,
		state:  ledger.NewState(),
	}, nil
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

type outputEnvelope struct {
	Pattern string          `json:"pattern"`
	Data    json.RawMessage `json:"data"`
}

// 형식 변환 후 Output WAL 작성
func (e *Engine) appendOutput(pattern string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal output data: %w", err)
	}
	payload, err := json.Marshal(outputEnvelope{Pattern: pattern, Data: raw})
	if err != nil {
		return fmt.Errorf("marshal output envelope: %w", err)
	}
	if _, err := e.output.Append(payload); err != nil {
		return fmt.Errorf("append output wal: %w", err)
	}
	return nil
}

func (e *Engine) handle(d Delivery) error {
	var env envelope
	if err := json.Unmarshal(d.Message.Payload, &env); err != nil {
		log.Printf("engine: decode envelope: %v", err)
		return d.Nack(false)
	}

	// Input WAL 작성
	if _, err := e.input.Append(d.Message.Payload); err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}

	switch env.Pattern {
	case PatternOrderCreated:
		return e.handleOrder(d, env.Data)
	default:
		log.Printf("engine: unknown pattern %q", env.Pattern)
		return d.Nack(false)
	}
}
