// 테스트 항목:
// - 송금 성공 시 발신·수신 잔액 이동과 account.updated 2건 + transfer.completed 이벤트 순서
// - 송금 성공 이벤트의 금액·계좌 정보와 처리 완료 시각 기록
// - 자기 계좌로 송금 시 SELF_TRANSFER 거부
// - 미등록·비정상 발신 계좌 송금 시 INVALID_SENDER 거부
// - 미등록·비정상 수신 계좌 송금 시 INVALID_RECIPIENT 거부
// - 가용 잔액을 초과한 송금 시 INSUFFICIENT_BALANCE 거부
// - 잘못된 요청 ID·금액 0 요청의 INVALID_REQUEST 거부
// - 거부는 error 가 아닌 transfer.rejected 이벤트 1건으로 기록되고 양쪽 잔액이 불변
package core

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

func TestTransferMovesBalancesAndAppendsCompletedEvents(t *testing.T) {
	e, output := newTransferTestEngine(t)
	e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 1000, AvailableBalance: 800})
	e.state.Accounts.Upsert(&domain.Account{Id: 2, Balance: 500, AvailableBalance: 500})

	transfer := domain.Transfer{Id: 77, SenderAccountId: 1, RecipientAccountId: 2, Amount: 300}
	if err := e.applyTransfer(transfer); err != nil {
		t.Fatalf("apply transfer: %v", err)
	}

	sender, ok := e.state.Accounts.Get(1)
	if !ok {
		t.Fatal("sender account not found after transfer")
	}
	if sender.Balance != 700 {
		t.Errorf("sender balance = %d, want 700", sender.Balance)
	}
	if sender.AvailableBalance != 500 {
		t.Errorf("sender available balance = %d, want 500", sender.AvailableBalance)
	}

	recipient, ok := e.state.Accounts.Get(2)
	if !ok {
		t.Fatal("recipient account not found after transfer")
	}
	if recipient.Balance != 800 {
		t.Errorf("recipient balance = %d, want 800", recipient.Balance)
	}
	if recipient.AvailableBalance != 800 {
		t.Errorf("recipient available balance = %d, want 800", recipient.AvailableBalance)
	}

	env := readOutputAt(t, output, 1)
	assertEventPatterns(t, env,
		PatternAccountUpdated,
		PatternAccountUpdated,
		PatternTransferCompleted,
	)

	var senderEvent domain.Account
	if err := json.Unmarshal(env.Events[0].Data, &senderEvent); err != nil {
		t.Fatalf("unmarshal sender account event: %v", err)
	}
	if senderEvent.Id != 1 || senderEvent.Balance != 700 {
		t.Errorf("sender event = %+v, want id 1 balance 700", senderEvent)
	}

	var recipientEvent domain.Account
	if err := json.Unmarshal(env.Events[1].Data, &recipientEvent); err != nil {
		t.Fatalf("unmarshal recipient account event: %v", err)
	}
	if recipientEvent.Id != 2 || recipientEvent.Balance != 800 {
		t.Errorf("recipient event = %+v, want id 2 balance 800", recipientEvent)
	}

	var completed domain.TransferCompleted
	if err := json.Unmarshal(env.Events[2].Data, &completed); err != nil {
		t.Fatalf("unmarshal transfer completed: %v", err)
	}
	if completed.Id != 77 {
		t.Errorf("completed id = %d, want 77", completed.Id)
	}
	if completed.SenderAccountId != 1 || completed.RecipientAccountId != 2 {
		t.Errorf("completed accounts = %d→%d, want 1→2", completed.SenderAccountId, completed.RecipientAccountId)
	}
	if completed.Amount != 300 {
		t.Errorf("completed amount = %d, want 300", completed.Amount)
	}
	if completed.CompletedAt.IsZero() {
		t.Error("completed at is zero, want processing time")
	}
}

func TestTransferRejectionsAppendSingleEventAndKeepBalances(t *testing.T) {
	tests := []struct {
		name     string
		transfer domain.Transfer
		reason   TransferRejectReason
	}{
		{
			name:     "self transfer",
			transfer: domain.Transfer{Id: 1, SenderAccountId: 1, RecipientAccountId: 1, Amount: 100},
			reason:   TransferRejectSelfTransfer,
		},
		{
			name:     "sender not found",
			transfer: domain.Transfer{Id: 2, SenderAccountId: 9, RecipientAccountId: 2, Amount: 100},
			reason:   TransferRejectInvalidSender,
		},
		{
			name:     "sender id not positive",
			transfer: domain.Transfer{Id: 7, SenderAccountId: -1, RecipientAccountId: 2, Amount: 100},
			reason:   TransferRejectInvalidSender,
		},
		{
			name:     "recipient not found",
			transfer: domain.Transfer{Id: 3, SenderAccountId: 1, RecipientAccountId: 9, Amount: 100},
			reason:   TransferRejectInvalidRecipient,
		},
		{
			name:     "recipient id not positive",
			transfer: domain.Transfer{Id: 4, SenderAccountId: 1, RecipientAccountId: -1, Amount: 100},
			reason:   TransferRejectInvalidRecipient,
		},
		{
			name:     "insufficient balance",
			transfer: domain.Transfer{Id: 5, SenderAccountId: 1, RecipientAccountId: 2, Amount: 801},
			reason:   TransferRejectInsufficientBalance,
		},
		{
			name:     "request id not positive",
			transfer: domain.Transfer{Id: 0, SenderAccountId: 1, RecipientAccountId: 2, Amount: 100},
			reason:   TransferRejectInvalidRequest,
		},
		{
			name:     "zero amount",
			transfer: domain.Transfer{Id: 6, SenderAccountId: 1, RecipientAccountId: 2, Amount: 0},
			reason:   TransferRejectInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, output := newTransferTestEngine(t)
			e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 1000, AvailableBalance: 800})
			e.state.Accounts.Upsert(&domain.Account{Id: 2, Balance: 500, AvailableBalance: 500})

			// 거부는 error 가 아니다. error 로 올라가면 Run 루프가 엔진을 종료시킨다.
			if err := e.applyTransfer(tt.transfer); err != nil {
				t.Fatalf("apply transfer returned error for rejection: %v", err)
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
			if rejected.Id != tt.transfer.Id {
				t.Errorf("rejected id = %d, want %d", rejected.Id, tt.transfer.Id)
			}
			if rejected.Reason != string(tt.reason) {
				t.Errorf("reject reason = %s, want %s", rejected.Reason, tt.reason)
			}
			if rejected.CompletedAt.IsZero() {
				t.Error("rejected completed at is zero, want processing time")
			}
		})
	}
}

func newTransferTestEngine(t *testing.T) (*Engine, *wal.WAL) {
	t.Helper()
	output, err := wal.Open(filepath.Join(t.TempDir(), "output"), nil)
	if err != nil {
		t.Fatalf("open output WAL: %v", err)
	}
	t.Cleanup(func() { _ = output.Close() })

	return &Engine{
		output:       output,
		state:        ledger.NewState(),
		inputSeq:     1,
		outputSignal: make(chan struct{}, 1),
	}, output
}
