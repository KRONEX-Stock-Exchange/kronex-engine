package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

const (
	// Input WAL: 수신 메세지 종류
	PatternOrderCreated = "order.created"

	// Output WAL: 발행 이벤트 종류
	PatternTradeExecuted  = "trade.executed"  // 체결 내역
	PatternOrderOpen      = "order.open"      // 호가창 등록(미체결/부분체결 잔량)
	PatternOrderFilled    = "order.filled"    // 전량 체결
	PatternOrderCanceled  = "order.canceled"  // 취소(시장가 미체결 잔량 등)
	PatternAccountUpdated = "account.updated" // 계좌 잔고 변동
	PatternHoldingUpdated = "holding.updated" // 보유종목 변동
)

const dedupWindow = 8192                 // 중복 방지 윈도우 크기
const snapshotInterval = 5 * time.Minute // 상태 스냅샷 주기

type snapshotData struct {
	state    []byte
	inputSeq uint64
}

type Engine struct {
	con    Consumer
	queue  string
	input  *wal.WAL
	output *wal.WAL
	state  *ledger.State
	store  SnapshotStore

	inputSeq         uint64            // 현재 처리 중인 입력 WAL 인덱스
	outputAppliedSeq uint64            // 출력에 이미 반영된 최대 입력 인덱스 (복구 워터마크)
	dedup            *dedup            // 요청 종류별 최근 처리 ID (큐 재전달 중복 방지)
	snapshots        chan snapshotData // 직렬화된 스냅샷 → DB 저장 goroutine 으로 전달
	outputSignal     chan struct{}     // Output WAL 새 레코드 알림 → 퍼블리셔 깨우기 (cap 1)
}

func NewEngine(con Consumer, store SnapshotStore, queue string) (*Engine, error) {
	input, err := wal.Open("./data/wal/input", nil)
	if err != nil {
		return nil, fmt.Errorf("open input wal: %w", err)
	}

	output, err := wal.Open("./data/wal/output", nil)
	if err != nil {
		return nil, fmt.Errorf("open output wal: %w", err)
	}

	e := &Engine{
		con:          con,
		queue:        queue,
		input:        input,
		output:       output,
		state:        ledger.NewState(),
		store:        store,
		dedup:        newDedup(dedupWindow),
		snapshots:    make(chan snapshotData, 1),
		outputSignal: make(chan struct{}, 1),
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

func (e *Engine) Replay(ctx context.Context) error {
	// 최신 스냅샷 로드
	var startIndex uint64
	if e.store != nil {
		state, idx, found, err := e.store.LatestSnapshot(ctx)
		if err != nil {
			return fmt.Errorf("load snapshot: %w", err)
		}
		if found {
			if err := e.state.Restore(state); err != nil {
				return fmt.Errorf("restore snapshot: %w", err)
			}
			startIndex = idx
		}
	}

	// InputWAL 로그 재생
	last, err := e.input.LastIndex()
	if err != nil {
		return fmt.Errorf("input last index: %w", err)
	}
	for i := startIndex + 1; i <= last; i++ {
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
			e.inputSeq = i

			// 주문 유효성 검사
			if err := e.validateOrder(order); err != nil {
				continue
			}
			if err := e.route(order); err != nil {
				return fmt.Errorf("replay route %d: %w", i, err)
			}
		}
	}

	return nil
}

func (e *Engine) Run(ctx context.Context) error {
	// 부팅 복구
	log.Printf("replay: start")
	if err := e.Replay(ctx); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	log.Printf("replay: success")

	deliveries, err := e.con.Deliveries(ctx, e.queue)
	if err != nil {
		return err
	}

	snapshotTick := time.NewTicker(snapshotInterval)
	defer snapshotTick.Stop()

	go e.runSnapshotSaver(ctx)

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
		case <-snapshotTick.C:
			if err := e.snapshot(); err != nil {
				log.Printf("engine: snapshot: %v", err)
			}
		}
	}
}

// CONSIDER: 스냅샷 저장시 불필요한 WAL 삭제 로직 필요
func (e *Engine) snapshot() error {
	data, err := e.state.Serialize()
	if err != nil {
		return fmt.Errorf("serialize state: %w", err)
	}
	snap := snapshotData{state: data, inputSeq: e.inputSeq}

	// DB Snapshot 저장
	select {
	case e.snapshots <- snap:
	default:
		log.Printf("engine: snapshot skipped (이전 저장 진행 중)")
	}
	return nil
}

// 직렬화된 스냅샷 DB 저장
func (e *Engine) runSnapshotSaver(ctx context.Context) {
	if e.store == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-e.snapshots:
			if err := e.store.SaveSnapshot(ctx, snap.state, snap.inputSeq); err != nil {
				log.Printf("engine: save snapshot: %v", err)
			}
		}
	}
}

type envelope struct {
	Pattern string          `json:"pattern"`
	Data    json.RawMessage `json:"data"`
}

type OutputEvent struct {
	Pattern string          `json:"pattern"`
	Data    json.RawMessage `json:"data"`
}

// 한 입력 주문이 만든 이벤트(체결들 + 상태)를 한 레코드로 묶은 Output WAL 봉투
type OutputEnvelope struct {
	InputSeq uint64        `json:"inputSeq"` // 이 출력을 만든 입력 WAL 인덱스
	Events   []OutputEvent `json:"events"`
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

	out := make([]OutputEvent, 0, len(events))
	for _, ev := range events {
		raw, err := json.Marshal(ev.data)
		if err != nil {
			return fmt.Errorf("marshal output data: %w", err)
		}
		out = append(out, OutputEvent{Pattern: ev.pattern, Data: raw})
	}
	payload, err := json.Marshal(OutputEnvelope{InputSeq: e.inputSeq, Events: out})
	if err != nil {
		return fmt.Errorf("marshal output envelope: %w", err)
	}
	if _, err := e.output.Append(payload); err != nil {
		return fmt.Errorf("append output wal: %w", err)
	}

	e.notifyPublisher()
	return nil
}

// Output WAL 공유
func (e *Engine) Output() *wal.WAL {
	return e.output
}

// 퍼블리셔가 받을 깨우기 신호 채널
func (e *Engine) OutputSignal() <-chan struct{} {
	return e.outputSignal
}

func (e *Engine) notifyPublisher() {
	select {
	case e.outputSignal <- struct{}{}:
	default:
	}
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
	var env OutputEnvelope
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
