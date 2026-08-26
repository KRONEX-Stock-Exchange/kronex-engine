// 테스트 항목:
// - 스냅샷 복원 후 스냅샷 이후 인덱스의 송금만 재생되고 스냅샷에 포함된 송금은 재적용되지 않음
// - 재생된 송금이 발신·수신 잔액을 이동시키고 account.updated 2건 + transfer.completed 를 기록
// - 거부된 송금도 재생 시 동일하게 transfer.rejected 1건을 기록하고 잔액이 불변
// - 출력 워터마크(outputAppliedSeq) 이하 입력은 상태만 복구하고 Output WAL 을 중복 기록하지 않음
package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

type fakeSnapshotStore struct {
	state []byte
	index uint64
	found bool
}

func (s *fakeSnapshotStore) LatestSnapshot(context.Context) ([]byte, uint64, bool, error) {
	return s.state, s.index, s.found, nil
}

func (s *fakeSnapshotStore) SaveSnapshotAndPrune(context.Context, []byte, uint64, int) (uint64, error) {
	return 0, nil
}

func (s *fakeSnapshotStore) PruneSnapshots(context.Context, int) (uint64, bool, error) {
	return 0, false, nil
}

func TestReplaySkipsTransfersAlreadyInSnapshot(t *testing.T) {
	// 스냅샷 시점: 1번 계좌가 이미 100 을 보낸 뒤의 잔액
	snapshot := ledger.NewState()
	snapshot.Accounts.Upsert(&domain.Account{Id: 1, Balance: 900, AvailableBalance: 700})
	snapshot.Accounts.Upsert(&domain.Account{Id: 2, Balance: 600, AvailableBalance: 600})

	e, input, output := newReplayTestEngine(t, snapshot, 1)

	// index 1: 스냅샷에 이미 반영된 송금 (재적용되면 안 됨)
	appendTransferInput(t, input, domain.Transfer{Id: 10, SenderAccountId: 1, RecipientAccountId: 2, Amount: 100})
	// index 2: 스냅샷 이후 송금 (재생 대상)
	appendTransferInput(t, input, domain.Transfer{Id: 11, SenderAccountId: 1, RecipientAccountId: 2, Amount: 300})

	if err := e.Replay(context.Background()); err != nil {
		t.Fatalf("replay: %v", err)
	}

	sender, ok := e.state.Accounts.Get(1)
	if !ok {
		t.Fatal("sender account not found after replay")
	}
	if sender.Balance != 600 || sender.AvailableBalance != 400 {
		t.Errorf("sender = %+v, want balance 600 available 400 (스냅샷 900/700 에서 300 만 이동)", sender)
	}

	recipient, ok := e.state.Accounts.Get(2)
	if !ok {
		t.Fatal("recipient account not found after replay")
	}
	if recipient.Balance != 900 || recipient.AvailableBalance != 900 {
		t.Errorf("recipient = %+v, want balance 900 available 900", recipient)
	}

	if e.inputSeq != 2 {
		t.Errorf("input seq = %d, want 2", e.inputSeq)
	}

	// 재생된 송금 1건에 대한 이벤트만 기록돼야 한다
	last, err := output.LastIndex()
	if err != nil {
		t.Fatalf("output last index: %v", err)
	}
	if last != 1 {
		t.Fatalf("output records = %d, want 1", last)
	}
	env := readOutputAt(t, output, 1)
	assertEventPatterns(t, env,
		PatternAccountUpdated,
		PatternAccountUpdated,
		PatternTransferCompleted,
	)
	if env.InputSeq != 2 {
		t.Errorf("output envelope input seq = %d, want 2", env.InputSeq)
	}

	var completed domain.TransferCompleted
	if err := json.Unmarshal(env.Events[2].Data, &completed); err != nil {
		t.Fatalf("unmarshal transfer completed: %v", err)
	}
	if completed.Id != 11 {
		t.Errorf("completed transfer id = %d, want 11", completed.Id)
	}
}

func TestReplayRecordsRejectedTransfer(t *testing.T) {
	snapshot := ledger.NewState()
	snapshot.Accounts.Upsert(&domain.Account{Id: 1, Balance: 1000, AvailableBalance: 800})
	snapshot.Accounts.Upsert(&domain.Account{Id: 2, Balance: 500, AvailableBalance: 500})

	e, input, output := newReplayTestEngine(t, snapshot, 0)

	// 가용 잔액을 넘는 송금 → 재생 시에도 거부돼야 한다
	appendTransferInput(t, input, domain.Transfer{Id: 20, SenderAccountId: 1, RecipientAccountId: 2, Amount: 801})

	if err := e.Replay(context.Background()); err != nil {
		t.Fatalf("replay: %v", err)
	}

	sender, _ := e.state.Accounts.Get(1)
	if sender.Balance != 1000 || sender.AvailableBalance != 800 {
		t.Errorf("sender = %+v, want balance 1000 available 800", sender)
	}
	recipient, _ := e.state.Accounts.Get(2)
	if recipient.Balance != 500 || recipient.AvailableBalance != 500 {
		t.Errorf("recipient = %+v, want balance 500 available 500", recipient)
	}

	env := readOutputAt(t, output, 1)
	assertEventPatterns(t, env, PatternTransferRejected)

	var rejected domain.TransferRejected
	if err := json.Unmarshal(env.Events[0].Data, &rejected); err != nil {
		t.Fatalf("unmarshal transfer rejected: %v", err)
	}
	if rejected.Reason != string(TransferRejectInsufficientBalance) {
		t.Errorf("reject reason = %s, want %s", rejected.Reason, TransferRejectInsufficientBalance)
	}
}

func TestReplayBelowOutputWatermarkRestoresStateWithoutDuplicateOutput(t *testing.T) {
	snapshot := ledger.NewState()
	snapshot.Accounts.Upsert(&domain.Account{Id: 1, Balance: 1000, AvailableBalance: 800})
	snapshot.Accounts.Upsert(&domain.Account{Id: 2, Balance: 500, AvailableBalance: 500})

	e, input, output := newReplayTestEngine(t, snapshot, 0)
	// 이 입력의 결과는 이미 Output WAL 에 기록된 상태로 재부팅했다고 가정
	e.outputAppliedSeq = 1

	appendTransferInput(t, input, domain.Transfer{Id: 30, SenderAccountId: 1, RecipientAccountId: 2, Amount: 300})

	if err := e.Replay(context.Background()); err != nil {
		t.Fatalf("replay: %v", err)
	}

	// 상태는 반드시 복구돼야 한다
	sender, _ := e.state.Accounts.Get(1)
	if sender.Balance != 700 || sender.AvailableBalance != 500 {
		t.Errorf("sender = %+v, want balance 700 available 500", sender)
	}
	recipient, _ := e.state.Accounts.Get(2)
	if recipient.Balance != 800 || recipient.AvailableBalance != 800 {
		t.Errorf("recipient = %+v, want balance 800 available 800", recipient)
	}

	// 이미 반영된 출력이므로 중복 기록되면 안 된다
	last, err := output.LastIndex()
	if err != nil {
		t.Fatalf("output last index: %v", err)
	}
	if last != 0 {
		t.Fatalf("output records = %d, want 0 (워터마크 이하 입력은 출력을 다시 쓰지 않는다)", last)
	}
}

func newReplayTestEngine(t *testing.T, snapshot *ledger.State, snapshotIdx uint64) (*Engine, *wal.WAL, *wal.WAL) {
	t.Helper()
	dir := t.TempDir()
	input, err := wal.Open(filepath.Join(dir, "input"), nil)
	if err != nil {
		t.Fatalf("open input WAL: %v", err)
	}
	t.Cleanup(func() { _ = input.Close() })

	output, err := wal.Open(filepath.Join(dir, "output"), nil)
	if err != nil {
		t.Fatalf("open output WAL: %v", err)
	}
	t.Cleanup(func() { _ = output.Close() })

	store := &fakeSnapshotStore{}
	if snapshot != nil {
		blob, err := snapshot.Serialize()
		if err != nil {
			t.Fatalf("serialize snapshot: %v", err)
		}
		store.state = blob
		store.index = snapshotIdx
		store.found = true
	}

	return &Engine{
		input:        input,
		output:       output,
		state:        ledger.NewState(),
		store:        store,
		dedup:        newDedup(dedupWindow),
		outputSignal: make(chan struct{}, 1),
	}, input, output
}

func appendTransferInput(t *testing.T, input *wal.WAL, transfer domain.Transfer) {
	t.Helper()
	data, err := json.Marshal(transfer)
	if err != nil {
		t.Fatalf("marshal transfer: %v", err)
	}
	raw, err := json.Marshal(envelope{Pattern: PatternTransferCreated, Data: data})
	if err != nil {
		t.Fatalf("marshal input envelope: %v", err)
	}
	if _, err := input.Append(raw); err != nil {
		t.Fatalf("append input WAL: %v", err)
	}
}
