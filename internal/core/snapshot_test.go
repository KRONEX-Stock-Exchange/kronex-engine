package core

import (
	"context"
	"testing"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
)

type saveCall struct {
	state []byte
	idx   uint64
}

// SnapshotStore 페이크: 저장 호출을 채널로 알린다.
type fakeSnapStore struct {
	saved chan saveCall
}

func (f *fakeSnapStore) SaveSnapshot(ctx context.Context, state []byte, inputWalIndex uint64) error {
	f.saved <- saveCall{state: append([]byte(nil), state...), idx: inputWalIndex}
	return nil
}

// [시나리오] 상태를 시드하고 inputSeq=42 로 snapshot() 호출
// [기대]    저장 goroutine 이 store.SaveSnapshot 을 호출, idx=42 + 직렬화된 상태가 복원 가능
func TestSnapshot_SerializesAndSaves(t *testing.T) {
	store := &fakeSnapStore{saved: make(chan saveCall, 1)}
	e := &Engine{
		state:     ledger.NewState(),
		store:     store,
		snapshots: make(chan snapshotData, 1),
	}
	putAccount(e, 1, 1_000_000)
	putStock(e, testStockID, 60_000, domain.LISTED)
	e.inputSeq = 42

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.runSnapshotSaver(ctx)

	if err := e.snapshot(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// 저장 goroutine 이 SaveSnapshot 을 호출할 때까지 대기 (1초 타임아웃)
	var call saveCall
	select {
	case call = <-store.saved:
	case <-time.After(time.Second):
		t.Fatal("스냅샷이 저장되지 않음 (SaveSnapshot 미호출)")
	}

	// 1) WAL 인덱스가 캡처 시점 그대로
	if call.idx != 42 {
		t.Fatalf("저장된 inputWalIndex = %d, want 42", call.idx)
	}
	// 2) 저장된 바이트가 비어있지 않고, 다시 상태로 복원 가능
	if len(call.state) == 0 {
		t.Fatal("저장된 state 가 비어있음")
	}
	restored := ledger.NewState()
	if err := restored.Restore(call.state); err != nil {
		t.Fatalf("restore: %v", err)
	}
	acc, ok := restored.Accounts.Get(1)
	if !ok || acc.Balance != 1_000_000 {
		t.Fatalf("복원된 계좌 = %+v (ok=%v), want balance=1000000", acc, ok)
	}
	if st, ok := restored.Stocks.Get(testStockID); !ok || st.Status != domain.LISTED {
		t.Fatalf("복원된 종목 = %+v (ok=%v), want LISTED", st, ok)
	}
}

// [시나리오] 저장이 밀려 채널이 찬 상태에서 snapshot() 또 호출
// [기대]    블록되지 않고 스킵 (hot path 안 막음)
func TestSnapshot_SkipsWhenSaverBusy(t *testing.T) {
	e := &Engine{
		state:     ledger.NewState(),
		snapshots: make(chan snapshotData, 1),
	}
	putStock(e, testStockID, 60_000, domain.LISTED)

	// 저장 goroutine 없음 → 채널을 먼저 가득 채움
	e.snapshots <- snapshotData{}

	done := make(chan error, 1)
	go func() { done <- e.snapshot() }() // 막히면 안 됨

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot() 이 블록됨 (스킵돼야 함)")
	}
}
