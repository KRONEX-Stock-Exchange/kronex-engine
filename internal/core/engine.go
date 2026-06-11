package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
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

// 중복 방지 윈도우 크기
const dedupWindow = 8192

type Engine struct {
	con    Consumer
	queue  string
	input  *wal.WAL
	output *wal.WAL
	state  *ledger.State

	inputSeq         uint64 // 현재 처리 중인 입력 WAL 인덱스
	outputAppliedSeq uint64 // 출력에 이미 반영된 최대 입력 인덱스 (복구 워터마크)
	dedup            *dedup // 요청 종류별 최근 처리 ID (큐 재전달 중복 방지)
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
		dedup:  newDedup(dedupWindow),
	}

	// 기존 Output WAL 에서 복구 워터마크 적재
	if err := e.loadOutputWatermark(); err != nil {
		return nil, fmt.Errorf("load output watermark: %w", err)
	}
	// 기존 Input WAL 에서 중복 방지 윈도우 복원
	if err := e.loadDedup(); err != nil {
		return nil, fmt.Errorf("load dedup window: %w", err)
	}

	return e, nil
}

// Input WAL를 읽어 dedup 복원
func (e *Engine) loadDedup() error {
	last, err := e.input.LastIndex()
	if err != nil {
		return fmt.Errorf("input last index: %w", err)
	}
	if last == 0 {
		return nil
	}

	// 읽어야 하는 인덱스 구하기
	// 만약에 last=10, window=3 이라면 8부터 읽도록
	start := uint64(1)
	if window := uint64(e.dedup.window); last > window {
		start = last - window + 1
	}
	for i := start; i <= last; i++ {
		data, err := e.input.Read(i)
		if err != nil {
			return fmt.Errorf("read input %d: %w", i, err)
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return fmt.Errorf("unmarshal input envelope %d: %w", i, err)
		}

		switch env.Pattern {
		case PatternOrderCreated:
			var order domain.Order
			if err := json.Unmarshal(env.Data, &order); err != nil {
				return fmt.Errorf("unmarshal order %d: %w", i, err)
			}
			e.dedup.add(env.Pattern, order.Id)
		}
	}
	return nil
}

func (e *Engine) Close() error {
	return errors.Join(e.input.Close(), e.output.Close())
}

func (e *Engine) Run(ctx context.Context) error {
	deliveries, err := e.con.Deliveries(ctx, e.queue)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return nil
			}
			if err := e.handle(d); err != nil {
				return err
			}
		}
	}
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

	switch env.Pattern {
	case PatternOrderCreated:
		return e.handleOrder(d, env.Data)
	default:
		log.Printf("engine: unknown pattern %q", env.Pattern)
		return d.Nack(false)
	}
}
