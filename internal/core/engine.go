package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

const (
	// Input WAL: 수신 메세지 종류
	PatternOrderCreated   = "order.created"   // 주문
	PatternAccountCreated = "account.created" // 계좌 등록

	// Input WAL: 어드민 요청 종류
	PatternStockList               = "stock.list"                 // 종목 상장 요청
	PatternAdminBalanceAdjust      = "admin.balance.adjust"       // 잔액 증감 요청
	PatternAdminStockBalanceAdjust = "admin.stock_balance.adjust" // 보유 주식 잔고 증감 요청

	// Output WAL: 발행 이벤트 종류
	PatternTradeExecuted    = "trade.executed"    // 체결 내역
	PatternOrderOpen        = "order.open"        // 호가창 등록(미체결/부분체결 잔량)
	PatternOrderFilled      = "order.filled"      // 전량 체결
	PatternOrderCanceled    = "order.canceled"    // 취소(시장가 미체결 잔량 등)
	PatternOrderRejected    = "order.rejected"    // 유효성 검사 실패로 거부
	PatternAccountUpdated   = "account.updated"   // 계좌 잔고 변동
	PatternAccountActivated = "account.activated" // 계좌 활성화
	PatternHoldingUpdated   = "holding.updated"   // 보유종목 변동
	PatternStockListed      = "stock.listed"      // 종목 상장 완료
	PatternStockUpdated     = "stock.updated"     // 종목 현재가 변동
	PatternOrderBookUpdated = "orderbook.updated" // 영향받은 호가 가격대의 최종 잔량
)

const dedupWindow = 8192                 // 중복 방지 윈도우 크기
const snapshotInterval = 5 * time.Minute // 상태 스냅샷 주기

type snapshotData struct {
	state    []byte
	inputSeq uint64
}

type Plane int

const (
	PlaneData  Plane = iota // 일반 요청 (계좌 등록·송금·주문)
	PlaneAdmin              // 어드민 요청 (상장·상폐·거래정지 등)
)

type Engine struct {
	con      Consumer
	routes   map[string]func(Delivery) error // 수신 큐 → 플레인 핸들러
	input    *wal.WAL
	output   *wal.WAL
	state    *ledger.State
	store    SnapshotStore
	tradeIDs TradeIDStore // NOTE: 기존 WAL 파일 양식에 맞추기 위한 임시 의존성

	inputSeq         uint64            // 현재 처리 중인 입력 WAL 인덱스
	outputAppliedSeq uint64            // 출력에 이미 반영된 최대 입력 인덱스 (복구 워터마크)
	lastTradeID      int64             // Output WAL에 기록된 마지막 엔진 발급 체결 ID
	dedup            *dedup            // 요청 종류별 최근 처리 ID (큐 재전달 중복 방지)
	snapshots        chan snapshotData // 직렬화된 스냅샷 → DB 저장 goroutine 으로 전달
	outputSignal     chan struct{}     // Output WAL 새 레코드 알림 → 퍼블리셔 깨우기 (cap 1)
}

func NewEngine(con Consumer, store SnapshotStore, tradeIDs TradeIDStore, queues map[string]Plane) (*Engine, error) {
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
		input:        input,
		output:       output,
		state:        ledger.NewState(),
		store:        store,
		tradeIDs:     tradeIDs,
		dedup:        newDedup(dedupWindow),
		snapshots:    make(chan snapshotData, 1),
		outputSignal: make(chan struct{}, 1),
	}

	// 큐 → 플레인 핸들러 라우팅 테이블 구성
	e.routes = make(map[string]func(Delivery) error, len(queues))
	for name, plane := range queues {
		switch plane {
		case PlaneData:
			e.routes[name] = e.handleData
		case PlaneAdmin:
			e.routes[name] = e.handleAdmin
		default:
			return nil, fmt.Errorf("unknown plane %d for queue %q", plane, name)
		}
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
		case PatternAccountCreated:
			var acc domain.Account
			if err := json.Unmarshal(env.Data, &acc); err != nil {
				return fmt.Errorf("unmarshal account %d: %w", i, err)
			}
			e.dedup.add(env.Pattern, int64(acc.Id))
		case PatternStockList:
			var stock domain.Stock
			if err := json.Unmarshal(env.Data, &stock); err != nil {
				return fmt.Errorf("unmarshal stock %d: %w", i, err)
			}
			e.dedup.add(env.Pattern, int64(stock.Id))
		case PatternAdminBalanceAdjust:
			var req domain.BalanceAdjust
			if err := json.Unmarshal(env.Data, &req); err != nil {
				return fmt.Errorf("unmarshal balance adjust %d: %w", i, err)
			}
			e.dedup.add(env.Pattern, req.Id)
		case PatternAdminStockBalanceAdjust:
			var req domain.StockBalanceAdjust
			if err := json.Unmarshal(env.Data, &req); err != nil {
				return fmt.Errorf("unmarshal stock balance adjust %d: %w", i, err)
			}
			e.dedup.add(env.Pattern, req.Id)
		}
	}
	return nil
}

func (e *Engine) Close() error {
	return errors.Join(e.input.Close(), e.output.Close())
}

func (e *Engine) Replay(ctx context.Context) error {
	// 최신 스냅샷 로드
	var lastSnapshotIdx uint64
	if e.store != nil {
		state, idx, found, err := e.store.LatestSnapshot(ctx)
		if err != nil {
			return fmt.Errorf("load snapshot: %w", err)
		}
		if found {
			if err := e.state.Restore(state); err != nil {
				return fmt.Errorf("restore snapshot: %w", err)
			}
			lastSnapshotIdx = idx
			e.inputSeq = lastSnapshotIdx
		}
	}

	// InputWAL 로그 재생
	lastIdx, err := e.input.LastIndex()
	if err != nil {
		return fmt.Errorf("input last index: %w", err)
	}
	for i := lastSnapshotIdx + 1; i <= lastIdx; i++ {
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
				if err := e.appendReject(order, err); err != nil {
					return fmt.Errorf("replay reject %d: %w", i, err)
				}
				continue
			}
			if err := e.route(order); err != nil {
				return fmt.Errorf("replay route %d: %w", i, err)
			}
		case PatternAccountCreated:
			var acc domain.Account
			if err := json.Unmarshal(env.Data, &acc); err != nil {
				return fmt.Errorf("unmarshal account %d: %w", i, err)
			}
			e.inputSeq = i
			e.activateAccount(acc)
		case PatternStockList:
			var stock domain.Stock
			if err := json.Unmarshal(env.Data, &stock); err != nil {
				return fmt.Errorf("unmarshal stock %d: %w", i, err)
			}
			e.inputSeq = i
			e.setStockStatus(stock, domain.LISTED, PatternStockListed)
		case PatternAdminBalanceAdjust:
			var req domain.BalanceAdjust
			if err := json.Unmarshal(env.Data, &req); err != nil {
				return fmt.Errorf("unmarshal balance adjust %d: %w", i, err)
			}
			e.inputSeq = i
			if _, err := e.applyBalanceAdjust(req); err != nil {
				return fmt.Errorf("replay balance adjust %d: %w", i, err)
			}
		case PatternAdminStockBalanceAdjust:
			var req domain.StockBalanceAdjust
			if err := json.Unmarshal(env.Data, &req); err != nil {
				return fmt.Errorf("unmarshal stock balance adjust %d: %w", i, err)
			}
			e.inputSeq = i
			if _, err := e.applyStockBalanceAdjust(req); err != nil {
				return fmt.Errorf("replay stock balance adjust %d: %w", i, err)
			}
		}
	}

	return nil
}

// 가장 최근 TradeID 조회
func (e *Engine) initializeTradeID(ctx context.Context) error {
	last, err := e.output.LastIndex()
	if err != nil {
		return fmt.Errorf("output last index: %w", err)
	}

	// Trade.executed를 찾을 때까지 역방향 탐색
	for i := last; i > 0; i-- {
		raw, err := e.output.Read(i)
		if err != nil {
			return fmt.Errorf("read output %d: %w", i, err)
		}
		var env OutputEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("unmarshal output envelope %d: %w", i, err)
		}
		for j := len(env.Events) - 1; j >= 0; j-- {
			ev := env.Events[j]
			if ev.Pattern != PatternTradeExecuted {
				continue
			}
			var trade domain.Trade
			if err := json.Unmarshal(ev.Data, &trade); err != nil {
				return fmt.Errorf("unmarshal trade output %d: %w", i, err)
			}
			if trade.Id > 0 {
				e.lastTradeID = trade.Id
				return nil
			}
		}
	}

	if e.tradeIDs == nil {
		return fmt.Errorf("load legacy trade id: no trade ID store")
	}
	lastTradeID, err := e.tradeIDs.LastTradeID(ctx)
	if err != nil {
		return fmt.Errorf("load legacy trade id: %w", err)
	}
	if lastTradeID < 0 {
		return fmt.Errorf("load legacy trade id: invalid id %d", lastTradeID)
	}
	e.lastTradeID = lastTradeID
	return nil
}

// TODO: 종목별 파티셔닝으로 변경될 경우 TradeID 형식을 변경해야됨
func (e *Engine) nextTradeID() (int64, error) {
	const maxTradeID = math.MaxInt64
	if e.lastTradeID == maxTradeID {
		return 0, fmt.Errorf("trade ID exhausted")
	}
	e.lastTradeID++
	return e.lastTradeID, nil
}

func (e *Engine) Run(ctx context.Context) error {
	if err := e.initializeTradeID(ctx); err != nil {
		return fmt.Errorf("initialize trade ID: %w", err)
	}

	// 부팅 복구
	log.Printf("replay: start")
	if err := e.Replay(ctx); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	log.Printf("replay: success")

	deliveries, err := e.consumeAll(ctx)
	if err != nil {
		return err
	}

	// 스냅샷 워커
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

// 구독 중인 모든 큐의 delivery 채널을 하나로 머지
func (e *Engine) consumeAll(ctx context.Context) (<-chan Delivery, error) {
	merged := make(chan Delivery)
	var wg sync.WaitGroup

	for q := range e.routes {
		ch, err := e.con.Deliveries(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("consume %q: %w", q, err)
		}
		wg.Add(1)
		go func(q string, ch <-chan Delivery) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case d, ok := <-ch:
					if !ok {
						return
					}
					d.Queue = q // 출처 큐 태깅
					select {
					case merged <- d:
					case <-ctx.Done():
						return
					}
				}
			}
		}(q, ch)
	}

	go func() {
		wg.Wait()
		close(merged)
	}()

	return merged, nil
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
	InputSeq  uint64        `json:"inputSeq"`  // 이 출력을 만든 Input WAL 인덱스
	OutputSeq uint64        `json:"outputSeq"` // 이 봉투가 기록된 Output WAL 인덱스
	CreatedAt time.Time     `json:"createdAt"` // Output WAL 기록 시각 (Publisher 지연 측정용)
	Events    []OutputEvent `json:"events"`
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
	createdAt := time.Now().UTC()
	if _, err := e.output.AppendWithIndex(func(outputSeq uint64) ([]byte, error) {
		return json.Marshal(OutputEnvelope{
			InputSeq:  e.inputSeq,
			OutputSeq: outputSeq,
			CreatedAt: createdAt,
			Events:    out,
		})
	}); err != nil {
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
