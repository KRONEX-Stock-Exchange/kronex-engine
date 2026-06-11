package core

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

// ── 헬퍼 ──────────────────────────────────────────────────────

func newEngineWithInput(t *testing.T) *Engine {
	t.Helper()
	in, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open input wal: %v", err)
	}
	out, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open output wal: %v", err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = out.Close() })
	return &Engine{input: in, output: out, state: ledger.NewState(), dedup: newDedup(dedupWindow)}
}

// 큐가 보내는 원본 주문 JSON(문자열 enum) 형식
func orderWire(o domain.Order) string {
	ot := "LIMIT"
	if o.OrderType == domain.ORDER_MARKET {
		ot = "MARKET"
	}
	tt := map[domain.TradingType]string{
		domain.TRADING_BUY: "BUY", domain.TRADING_SELL: "SELL",
		domain.TRADING_EDIT: "EDIT", domain.TRADING_CANCEL: "CANCEL",
	}[o.TradingType]
	return fmt.Sprintf(
		`{"id":"%d","targetId":"%d","accountId":%d,"stockId":%d,"price":"%d","quantity":"%d","filledQuantity":"%d","orderType":"%s","tradingType":"%s"}`,
		o.Id, o.TargetId, o.AccountId, o.StockId, o.Price, o.Quantity, o.FilledQuantity, ot, tt,
	)
}

func fakeDelivery(t *testing.T, o domain.Order, acks, nacks *int) Delivery {
	t.Helper()
	env, err := json.Marshal(envelope{Pattern: PatternOrderCreated, Data: json.RawMessage(orderWire(o))})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return Delivery{
		Message: domain.Message{Payload: env},
		Ack:     func() error { *acks++; return nil },
		Nack:    func(requeue bool) error { *nacks++; return nil },
	}
}

// ── recentIDs 단위 ─────────────────────────────────────────────

// [시나리오] 윈도우 3 에 1,2,3,4 순으로 add
// [기대]    가장 오래된 1 축출, 2/3/4 유지, 중복 add 는 축출을 일으키지 않음
func TestRecentIDs_HasAndEviction(t *testing.T) {
	r := newRecentIDs(3)
	r.add(1)
	r.add(2)
	r.add(3)
	if !r.has(1) || !r.has(2) || !r.has(3) {
		t.Fatalf("1,2,3 모두 있어야 함")
	}
	r.add(4) // 가장 오래된 1 축출
	if r.has(1) {
		t.Fatalf("1 은 축출돼야 함")
	}
	if !r.has(2) || !r.has(3) || !r.has(4) {
		t.Fatalf("2,3,4 는 있어야 함")
	}
	r.add(4) // 중복 add → 아무 축출 없음
	if !r.has(2) {
		t.Fatalf("중복 add 가 2 를 축출하면 안 됨")
	}
}

// [시나리오] 같은 ID(5) 를 서로 다른 종류(order/command)에 add
// [기대]    종류별 윈도우가 독립 → 한쪽에 있어도 다른 쪽엔 없음
func TestDedup_KindsAreIndependent(t *testing.T) {
	d := newDedup(8)
	d.add("order.created", 5)

	if !d.has("order.created", 5) {
		t.Fatalf("order 종류에 5 가 있어야 함")
	}
	if d.has("system.command", 5) {
		t.Fatalf("같은 ID 5 라도 command 종류엔 없어야 함 (윈도우 독립)")
	}
	// 없는 종류 조회는 그냥 false
	if d.has("unknown", 1) {
		t.Fatalf("등록 안 된 종류는 항상 false")
	}
}

// ── handleOrder 중복 처리 ───────────────────────────────────────

// [시나리오] 같은 주문 id=10 을 두 번 전달 (재전달 흉내)
// [기대]    첫 번째만 Input WAL 기록·처리, 두 번째는 Ack 후 버림 (WAL 미증가, 중복 처리 없음)
func TestHandleOrder_DuplicateDropped(t *testing.T) {
	e := newEngineWithInput(t)
	putAccount(e, 100, 100_000_000)
	putStock(e, testStockID, 60_000, domain.LISTED)

	order := ord(10, 100, domain.TRADING_BUY, 70_000, 50)

	var acks, nacks int
	d1 := fakeDelivery(t, order, &acks, &nacks)
	if err := e.handle(d1); err != nil {
		t.Fatalf("handle 1: %v", err)
	}
	if li, _ := e.input.LastIndex(); li != 1 {
		t.Fatalf("input = %d, want 1 (1차 기록)", li)
	}
	if acks != 1 || nacks != 0 {
		t.Fatalf("ack/nack = %d/%d, want 1/0", acks, nacks)
	}

	// 재전달: 같은 id 다시
	d2 := fakeDelivery(t, order, &acks, &nacks)
	if err := e.handle(d2); err != nil {
		t.Fatalf("handle 2: %v", err)
	}
	if li, _ := e.input.LastIndex(); li != 1 {
		t.Fatalf("input = %d, want 1 (중복은 WAL 미기록)", li)
	}
	if acks != 2 || nacks != 0 {
		t.Fatalf("ack/nack = %d/%d, want 2/0 (중복도 Ack 로 버림)", acks, nacks)
	}
}

// [시나리오] 재시작: id=10 기록된 Input WAL 로 새 엔진을 열고 같은 id 재전달
// [기대]    loadDedup 으로 윈도우 복원 → 재전달이 중복으로 걸러짐
func TestHandleOrder_DedupSurvivesRestart(t *testing.T) {
	inDir := t.TempDir()
	outDir := t.TempDir()

	open := func() *Engine {
		in, err := wal.Open(inDir, nil)
		if err != nil {
			t.Fatalf("open input: %v", err)
		}
		out, err := wal.Open(outDir, nil)
		if err != nil {
			t.Fatalf("open output: %v", err)
		}
		e := &Engine{input: in, output: out, state: ledger.NewState(), dedup: newDedup(dedupWindow)}
		if err := e.loadDedup(); err != nil {
			t.Fatalf("load dedup: %v", err)
		}
		return e
	}

	order := ord(10, 100, domain.TRADING_BUY, 70_000, 50)

	// 1차: 처리해서 Input WAL 에 기록
	e1 := open()
	putAccount(e1, 100, 100_000_000)
	putStock(e1, testStockID, 60_000, domain.LISTED)
	var a1, n1 int
	if err := e1.handle(fakeDelivery(t, order, &a1, &n1)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	_ = e1.Close()

	// 재시작: 같은 WAL 로 다시 열면 loadDedup 으로 id=10 복원
	e2 := open()
	if !e2.dedup.has(PatternOrderCreated, 10) {
		t.Fatalf("재시작 후 id=10 이 dedup 윈도우에 복원돼야 함")
	}
	var a2, n2 int
	if err := e2.handle(fakeDelivery(t, order, &a2, &n2)); err != nil {
		t.Fatalf("handle after restart: %v", err)
	}
	if li, _ := e2.input.LastIndex(); li != 1 {
		t.Fatalf("input = %d, want 1 (재시작 후에도 중복 미기록)", li)
	}
	if a2 != 1 {
		t.Fatalf("재전달 ack = %d, want 1 (중복 버림)", a2)
	}
	_ = e2.Close()
}
