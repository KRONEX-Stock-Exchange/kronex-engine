package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

// [시나리오] 스냅샷(계좌/종목, index=0) + Input WAL 의 매수 주문 2건(idx 1,2)
//
//	→ Replay: 스냅샷 복원 후 1,2 재생
//
// [기대] 계좌/종목 복원됨, 두 주문이 호가창에 재구성됨, 각 주문의 Output(order.open) 생성, inputSeq=2
func TestReplay_RestoresSnapshotThenReplaysInput(t *testing.T) {
	in, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open input wal: %v", err)
	}
	out, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open output wal: %v", err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = out.Close() })

	// 스냅샷 상태: 계좌 100 + 종목 (index 0 = 아직 주문 처리 전)
	snapState := ledger.NewState()
	snapState.Accounts.Upsert(&domain.Account{Id: 100, Balance: 100_000_000, AvailableBalance: 100_000_000})
	snapState.Stocks.Upsert(&domain.Stock{Id: testStockID, Price: 60_000, Status: domain.LISTED})
	snapBytes, err := snapState.Serialize()
	if err != nil {
		t.Fatalf("serialize snapshot: %v", err)
	}
	store := &fakeSnapStore{loadState: snapBytes, loadFound: true} // loadIdx=0

	// Input WAL: 매수 2건 (대기 매도 없음 → 둘 다 호가창에 등록)
	appendOrder := func(o domain.Order) {
		env, err := json.Marshal(envelope{Pattern: PatternOrderCreated, Data: json.RawMessage(orderWire(o))})
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		if _, err := in.Append(env); err != nil {
			t.Fatalf("append input: %v", err)
		}
	}
	appendOrder(ord(10, 100, domain.TRADING_BUY, 70_000, 50))
	appendOrder(ord(11, 100, domain.TRADING_BUY, 69_000, 50))

	e := &Engine{
		input:     in,
		output:    out,
		state:     ledger.NewState(),
		store:     store,
		dedup:     newDedup(dedupWindow),
		snapshots: make(chan snapshotData, 1),
	}
	if err := e.loadOutputWatermark(); err != nil { // 출력 비어있음 → 워터마크 0
		t.Fatalf("load watermark: %v", err)
	}

	if err := e.Replay(context.Background()); err != nil {
		t.Fatalf("replay: %v", err)
	}

	// 1) 스냅샷 복원: 계좌/종목 살아있음
	if _, ok := e.state.Accounts.Get(100); !ok {
		t.Fatal("스냅샷의 계좌가 복원되지 않음")
	}
	if _, ok := e.state.Stocks.Get(testStockID); !ok {
		t.Fatal("스냅샷의 종목이 복원되지 않음")
	}
	// 2) WAL 재생: 두 주문이 호가창에 재구성
	ob := e.state.OrderBooks.Get(testStockID)
	if _, ok := ob.Get(10); !ok {
		t.Fatal("주문 10 이 재생되지 않음")
	}
	if _, ok := ob.Get(11); !ok {
		t.Fatal("주문 11 이 재생되지 않음")
	}
	// 3) 각 주문의 Output(order.open) 생성, inputSeq 마지막 값
	envs := outputEnvelopesOf(t, e)
	if len(envs) != 2 {
		t.Fatalf("재생 후 output = %d, want 2 (order.open x2)", len(envs))
	}
	if e.inputSeq != 2 {
		t.Fatalf("inputSeq = %d, want 2", e.inputSeq)
	}
}

// [시나리오] 스냅샷 없음(found=false) + Input WAL 주문 1건
// [기대]    빈 상태에서 1번부터 재생 (스냅샷 없어도 동작)
func TestReplay_NoSnapshotReplaysFromStart(t *testing.T) {
	in, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open input wal: %v", err)
	}
	out, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open output wal: %v", err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = out.Close() })

	e := &Engine{
		input:     in,
		output:    out,
		state:     ledger.NewState(),
		store:     &fakeSnapStore{loadFound: false}, // 스냅샷 없음
		dedup:     newDedup(dedupWindow),
		snapshots: make(chan snapshotData, 1),
	}
	// 계좌/종목 없이 들어온 주문 → 유효성 실패 → 재생에서 건너뜀(에러 아님)
	env, _ := json.Marshal(envelope{Pattern: PatternOrderCreated, Data: json.RawMessage(orderWire(ord(1, 100, domain.TRADING_BUY, 70_000, 10)))})
	if _, err := in.Append(env); err != nil {
		t.Fatalf("append input: %v", err)
	}

	if err := e.Replay(context.Background()); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if e.inputSeq != 1 {
		t.Fatalf("inputSeq = %d, want 1 (재생은 됨)", e.inputSeq)
	}
}

// 테스트용: 계좌(100, 1억) + 종목을 담은 스냅샷 바이트
func seededSnapshotBytes(t *testing.T) []byte {
	t.Helper()
	s := ledger.NewState()
	s.Accounts.Upsert(&domain.Account{Id: 100, Balance: 100_000_000, AvailableBalance: 100_000_000})
	s.Stocks.Upsert(&domain.Stock{Id: testStockID, Price: 60_000, Status: domain.LISTED})
	b, err := s.Serialize()
	if err != nil {
		t.Fatalf("serialize snapshot: %v", err)
	}
	return b
}

func openWALs(t *testing.T) (in, out *wal.WAL) {
	t.Helper()
	in, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open input wal: %v", err)
	}
	out, err = wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open output wal: %v", err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = out.Close() })
	return in, out
}

// [시나리오] 스냅샷이 index=2 까지 반영됐다고 주장 + Input WAL 주문 3건(idx 1,2,3)
// [기대]    idx<=2(주문 1,2)는 재생하지 않고, 주문 3만 재생 (스냅샷 인덱스 존중, 이중 적용 방지)
func TestReplay_SkipsInputsCoveredBySnapshot(t *testing.T) {
	in, out := openWALs(t)
	store := &fakeSnapStore{loadState: seededSnapshotBytes(t), loadIdx: 2, loadFound: true}

	for _, o := range []domain.Order{
		ord(1, 100, domain.TRADING_BUY, 70_000, 10),
		ord(2, 100, domain.TRADING_BUY, 69_000, 10),
		ord(3, 100, domain.TRADING_BUY, 68_000, 10),
	} {
		if _, err := in.Append(orderEnv(t, o)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	e := &Engine{
		input: in, output: out, state: ledger.NewState(),
		store: store, dedup: newDedup(dedupWindow), snapshots: make(chan snapshotData, 1),
	}
	if err := e.loadOutputWatermark(); err != nil {
		t.Fatalf("load watermark: %v", err)
	}
	if err := e.Replay(context.Background()); err != nil {
		t.Fatalf("replay: %v", err)
	}

	ob := e.state.OrderBooks.Get(testStockID)
	if _, ok := ob.Get(3); !ok {
		t.Fatal("주문 3 (idx>스냅샷) 이 재생되지 않음")
	}
	if _, ok := ob.Get(1); ok {
		t.Fatal("주문 1 (idx<=스냅샷) 이 재생됨 — 이중 적용")
	}
	if _, ok := ob.Get(2); ok {
		t.Fatal("주문 2 (idx<=스냅샷) 가 재생됨 — 이중 적용")
	}
	if e.inputSeq != 3 {
		t.Fatalf("inputSeq = %d, want 3", e.inputSeq)
	}
}

// [시나리오] 스냅샷 index=0 + Input 주문 2건, 그러나 Output 에 주문1 출력이 이미 있음(워터마크=1)
// [기대]    재생 시 주문 1,2 모두 상태 재구성 / Output 은 주문1은 스킵(중복 방지), 주문2만 신규 작성
func TestReplay_SkipsAlreadyEmittedOutputDuringReplay(t *testing.T) {
	in, out := openWALs(t)
	store := &fakeSnapStore{loadState: seededSnapshotBytes(t), loadIdx: 0, loadFound: true}

	for _, o := range []domain.Order{
		ord(1, 100, domain.TRADING_BUY, 70_000, 10),
		ord(2, 100, domain.TRADING_BUY, 69_000, 10),
	} {
		if _, err := in.Append(orderEnv(t, o)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	e := &Engine{
		input: in, output: out, state: ledger.NewState(),
		store: store, dedup: newDedup(dedupWindow), snapshots: make(chan snapshotData, 1),
	}
	// 주문1의 Output 을 미리 작성 (크래시 전 출력됨을 모사) → inputSeq=1
	e.inputSeq = 1
	if err := e.appendOutput(outEvent{PatternOrderOpen, domain.OrderEvent{OrderId: 1}}); err != nil {
		t.Fatalf("seed output: %v", err)
	}
	if err := e.loadOutputWatermark(); err != nil { // 워터마크 = 1
		t.Fatalf("load watermark: %v", err)
	}

	if err := e.Replay(context.Background()); err != nil {
		t.Fatalf("replay: %v", err)
	}

	// 상태: 주문 1,2 모두 호가창에 재구성
	ob := e.state.OrderBooks.Get(testStockID)
	if _, ok := ob.Get(1); !ok {
		t.Fatal("주문 1 상태 재구성 안됨")
	}
	if _, ok := ob.Get(2); !ok {
		t.Fatal("주문 2 상태 재구성 안됨")
	}

	// Output: 주문1(미리 작성, inputSeq1) + 주문2(재생 신규, inputSeq2) = 2건. 주문1 중복 없음.
	envs := outputEnvelopesOf(t, e)
	if len(envs) != 2 {
		t.Fatalf("output = %d, want 2 (주문1 스킵, 주문2 신규)", len(envs))
	}
	if envs[0].InputSeq != 1 || envs[1].InputSeq != 2 {
		t.Fatalf("output inputSeq = [%d, %d], want [1, 2]", envs[0].InputSeq, envs[1].InputSeq)
	}
}

// 미리 채운 채널을 그대로 돌려주는 가짜 Consumer. 채널이 닫히면 Run 이 종료된다.
type fakeConsumer struct {
	ch <-chan Delivery
}

func (f *fakeConsumer) Deliveries(ctx context.Context, queue string) (<-chan Delivery, error) {
	return f.ch, nil
}

// envelope(order.created) 바이트 (Input WAL 직접 기록용)
func orderEnv(t *testing.T, o domain.Order) []byte {
	t.Helper()
	b, err := json.Marshal(envelope{Pattern: PatternOrderCreated, Data: json.RawMessage(orderWire(o))})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

// [시나리오] 크래시-복구 전 과정:
//  1. A(id=10) 처리 → Input WAL idx1 + Output + 상태. 이 시점 스냅샷(idx=1) 저장.
//  2. B(id=11) 는 Input WAL idx2 만 기록되고 엔진 사망 (Output/상태 없음).
//  3. 재시작: 스냅샷 로드(A 복원) + Replay 로 B 재처리(누락 Output 복구).
//  4. 라이브 큐: 새 주문 C(id=12) + 중복 B(id=11) 수신.
//
// [기대]
//   - 호가창에 A,B,C 모두 존재 (B 는 Input-only 였지만 복구됨)
//   - Output 은 A,B,C 각 1건(order.open) — B 중복/누락 없음
//   - 중복 B 는 dedup 으로 버려져 Input WAL 미증가(=3), Ack 처리
func TestRecovery_ReplayThenLiveWithDuplicate(t *testing.T) {
	inDir := t.TempDir()
	outDir := t.TempDir()

	// ── Phase 1: A 처리 + 스냅샷, B 는 Input 만 기록 후 "크래시" ──
	in1, err := wal.Open(inDir, nil)
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	out1, err := wal.Open(outDir, nil)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	e1 := &Engine{
		input: in1, output: out1, state: ledger.NewState(),
		dedup: newDedup(dedupWindow), snapshots: make(chan snapshotData, 1),
	}
	putAccount(e1, 100, 100_000_000)
	putStock(e1, testStockID, 60_000, domain.LISTED)

	// A: 매수 50 @ 70000 (대기 매도 없음 → 호가창 등록) — Input idx1, Output(order.open)
	var a1, n1 int
	if err := e1.handle(fakeDelivery(t, ord(10, 100, domain.TRADING_BUY, 70_000, 50), &a1, &n1)); err != nil {
		t.Fatalf("handle A: %v", err)
	}

	// A 처리 직후 스냅샷 (상태 + inputSeq=1)
	snapBytes, err := e1.state.Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	snapIdx := e1.inputSeq // = 1

	// B: Input WAL 에만 기록하고 죽음 (route 안 함 → Output/상태 없음) — Input idx2
	if _, err := in1.Append(orderEnv(t, ord(11, 100, domain.TRADING_BUY, 69_000, 50))); err != nil {
		t.Fatalf("append B: %v", err)
	}
	_ = in1.Close()
	_ = out1.Close()

	// ── Phase 2+3: 재시작 → 같은 WAL + 스냅샷 store + 라이브 큐 ──
	in2, err := wal.Open(inDir, nil)
	if err != nil {
		t.Fatalf("reopen input: %v", err)
	}
	out2, err := wal.Open(outDir, nil)
	if err != nil {
		t.Fatalf("reopen output: %v", err)
	}
	t.Cleanup(func() { _ = in2.Close(); _ = out2.Close() })

	// 라이브 배달: 새 주문 C + 중복 B (이미 처리/복구된 id=11)
	ch := make(chan Delivery, 2)
	var aLive, nLive int
	ch <- fakeDelivery(t, ord(12, 100, domain.TRADING_BUY, 68_000, 50), &aLive, &nLive) // C: 신규
	ch <- fakeDelivery(t, ord(11, 100, domain.TRADING_BUY, 69_000, 50), &aLive, &nLive) // B: 중복
	close(ch)

	store := &fakeSnapStore{loadState: snapBytes, loadIdx: snapIdx, loadFound: true}
	e2 := &Engine{
		con:   &fakeConsumer{ch: ch},
		queue: "q",
		input: in2, output: out2, state: ledger.NewState(),
		store: store, dedup: newDedup(dedupWindow), snapshots: make(chan snapshotData, 1),
	}
	// NewEngine 부팅 시퀀스 모방: 출력 워터마크 + dedup 윈도우 복원
	if err := e2.loadOutputWatermark(); err != nil {
		t.Fatalf("load watermark: %v", err)
	}
	if err := e2.loadDedup(); err != nil {
		t.Fatalf("load dedup: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Run: Replay(스냅샷 복원 + B 재처리) → 라이브 C 처리 + 중복 B 폐기 → 채널 닫힘 → 종료
	if err := e2.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// ── 검증 ──
	ob := e2.state.OrderBooks.Get(testStockID)
	for _, id := range []int64{10, 11, 12} {
		if _, ok := ob.Get(id); !ok {
			t.Fatalf("주문 %d 가 호가창에 없음 (복구/처리 누락)", id)
		}
	}

	// 중복 B 는 Input WAL 에 안 들어감 → idx 3 (A,B,C)
	if li, _ := e2.input.LastIndex(); li != 3 {
		t.Fatalf("input last = %d, want 3 (중복은 미기록)", li)
	}

	// Output: A,B,C 각 order.open 1건씩, inputSeq 1/2/3, 중복/누락 없음
	envs := outputEnvelopesOf(t, e2)
	if len(envs) != 3 {
		t.Fatalf("output = %d, want 3 (A,B,C order.open)", len(envs))
	}
	for i, want := range []uint64{1, 2, 3} {
		if envs[i].Pattern != PatternOrderOpen || envs[i].InputSeq != want {
			t.Fatalf("output[%d] = {%s, seq=%d}, want {order.open, seq=%d}", i, envs[i].Pattern, envs[i].InputSeq, want)
		}
	}

	// 라이브 2건(C 처리 + 중복 B 폐기) 모두 Ack
	if aLive != 2 || nLive != 0 {
		t.Fatalf("라이브 ack/nack = %d/%d, want 2/0", aLive, nLive)
	}
}
