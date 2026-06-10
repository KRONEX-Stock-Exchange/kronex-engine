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
	// Input WAL: 수신 메세지 종류
	PatternOrderCreated = "order.created"

	// Output WAL: 발행 이벤트 종류
	PatternTradeExecuted = "trade.executed" // 체결 내역
	PatternOrderOpen     = "order.open"     // 호가창 등록(미체결/부분체결 잔량)
	PatternOrderFilled   = "order.filled"   // 전량 체결
	PatternOrderCanceled = "order.canceled" // 취소(시장가 미체결 잔량 등)
)

type Engine struct {
	con    Consumer
	queue  string
	input  *wal.WAL
	output *wal.WAL
	state  *ledger.State

	inputSeq         uint64 // 현재 처리 중인 입력 WAL 인덱스
	outputAppliedSeq uint64 // 출력에 이미 반영된 최대 입력 인덱스 (복구 워터마크)
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

	e := &Engine{
		con:    con,
		queue:  queue,
		input:  input,
		output: output,
		state:  ledger.NewState(),
	}

	// 기존 Output WAL 로부터 복구 워터마크 적재
	if err := e.loadOutputWatermark(); err != nil {
		return nil, fmt.Errorf("load output watermark: %w", err)
	}

	return e, nil
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
	Pattern  string          `json:"pattern"`
	InputSeq uint64          `json:"inputSeq"` // 이 출력을 만든 입력 WAL 인덱스
	Data     json.RawMessage `json:"data"`
}

// Output WAL 형태로 변환 (inputSeq = 이 출력을 만든 입력 WAL 인덱스)
func marshalOutput(pattern string, inputSeq uint64, data any) ([]byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal output data: %w", err)
	}
	payload, err := json.Marshal(outputEnvelope{Pattern: pattern, InputSeq: inputSeq, Data: raw})
	if err != nil {
		return nil, fmt.Errorf("marshal output envelope: %w", err)
	}
	return payload, nil
}

type outEvent struct {
	pattern string
	data    any
}

// Output WAL 생성
func (e *Engine) appendOutput(events ...outEvent) error {
	if len(events) == 0 {
		return nil
	}

	// 이벤트 복구 중 중복 Output 생성 방지
	if e.outputAppliedSeq > 0 && e.inputSeq <= e.outputAppliedSeq {
		return nil
	}

	payloads := make([][]byte, 0, len(events))
	for _, ev := range events {
		payload, err := marshalOutput(ev.pattern, e.inputSeq, ev.data)
		if err != nil {
			return err
		}
		payloads = append(payloads, payload)
	}
	if _, err := e.output.AppendBatch(payloads); err != nil {
		return fmt.Errorf("append output wal batch: %w", err)
	}
	return nil
}

// 마지막 Output WAL에서 마지막으로 처리된 Input WAL를
// 읽어 복구 워터마크(outputAppliedSeq)로 설정함 출력이 비어 있으면 0
func (e *Engine) loadOutputWatermark() error {
	last, err := e.output.LastIndex()
	if err != nil {
		return fmt.Errorf("output last index: %w", err)
	}
	if last == 0 {
		e.outputAppliedSeq = 0
		return nil
	}

	data, err := e.output.Read(last)
	if err != nil {
		return fmt.Errorf("read output %d: %w", last, err)
	}
	var env outputEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("unmarshal output envelope %d: %w", last, err)
	}
	e.outputAppliedSeq = env.InputSeq
	return nil
}

func (e *Engine) handle(d Delivery) error {
	var env envelope
	if err := json.Unmarshal(d.Message.Payload, &env); err != nil {
		log.Printf("engine: decode envelope: %v", err)
		return d.Nack(false)
	}

	// Input WAL 작성 (인덱스를 현재 처리 시퀀스로 기록 → 출력 워터마크 기준)
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx

	switch env.Pattern {
	case PatternOrderCreated:
		return e.handleOrder(d, env.Data)
	default:
		log.Printf("engine: unknown pattern %q", env.Pattern)
		return d.Nack(false)
	}
}
