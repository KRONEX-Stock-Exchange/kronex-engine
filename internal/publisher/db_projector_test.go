// 테스트 항목:
// - 원주문 order.canceled 이벤트를 CANCELED 상태로 반영
// - 취소 요청 order.completed 이벤트를 COMPLETED 상태로 반영
// - 두 주문 상태와 DB 적용 커서를 하나의 트랜잭션에서 커밋
// - 정정 원주문 REPLACED와 대체 주문의 누적 수량·OPEN 상태를 하나의 트랜잭션에서 반영
// - transfer.completed·transfer.rejected 이벤트를 송금 완료·거부 처리로 반영
package publisher

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

type orderStatusRecordingTx struct {
	Tx
	updates     []orderStatusUpdate
	cursor      uint64
	committed   bool
	rollbackRun bool
}

type orderStatusUpdate struct {
	orderID       int64
	status        string
	quantity      uint64
	filledQty     uint64
	fullyFilledAt *time.Time
}

func (t *orderStatusRecordingTx) UpdateOrderState(_ context.Context, orderID int64, status string, quantity, filledQty uint64, fullyFilledAt *time.Time) error {
	t.updates = append(t.updates, orderStatusUpdate{orderID, status, quantity, filledQty, fullyFilledAt})
	return nil
}

func (t *orderStatusRecordingTx) SaveDBAppliedCursor(_ context.Context, index uint64) error {
	t.cursor = index
	return nil
}

func (t *orderStatusRecordingTx) Commit() error {
	t.committed = true
	return nil
}

func (t *orderStatusRecordingTx) Rollback() error {
	t.rollbackRun = true
	return nil
}

type orderStatusRecordingStore struct {
	tx         *orderStatusRecordingTx
	beginCount int
}

func (s *orderStatusRecordingStore) Begin(context.Context) (Tx, error) {
	s.beginCount++
	return s.tx, nil
}

func (s *orderStatusRecordingStore) LoadDBAppliedCursor(context.Context) (uint64, error) {
	return 0, nil
}

func TestDBProjectorAppliesCanceledAndCompletedInOneTransaction(t *testing.T) {
	events := []core.OutputEvent{
		orderStatusOutputEvent(t, core.PatternOrderCanceled, domain.OrderEvent{Id: 10, Quantity: 10, FilledQuantity: 3}),
		orderStatusOutputEvent(t, core.PatternOrderCompleted, domain.OrderEvent{Id: 20}),
	}
	env := core.OutputEnvelope{InputSeq: 1, OutputSeq: 1, Events: events}
	tx := &orderStatusRecordingTx{}
	store := &orderStatusRecordingStore{tx: tx}
	projector := &DBProjector{store: store}

	if err := projector.applyBatch(context.Background(), 7, []core.OutputEnvelope{env}); err != nil {
		t.Fatalf("apply output envelope: %v", err)
	}

	want := []orderStatusUpdate{
		{orderID: 10, status: "CANCELED", quantity: 10, filledQty: 3},
		{orderID: 20, status: "COMPLETED", quantity: 0, filledQty: 0},
	}
	if len(tx.updates) != len(want) {
		t.Fatalf("status updates = %+v, want %+v", tx.updates, want)
	}
	for i := range want {
		if tx.updates[i] != want[i] {
			t.Errorf("status update[%d] = %+v, want %+v", i, tx.updates[i], want[i])
		}
	}
	if store.beginCount != 1 {
		t.Errorf("transaction begin count = %d, want 1", store.beginCount)
	}
	if !tx.committed {
		t.Error("transaction was not committed")
	}
	if tx.cursor != 7 {
		t.Errorf("saved DB cursor = %d, want 7", tx.cursor)
	}
	if !tx.rollbackRun {
		t.Error("deferred rollback was not called")
	}
}

func TestDBProjectorAppliesReplacedAndOpenInOneTransaction(t *testing.T) {
	events := []core.OutputEvent{
		orderStatusOutputEvent(t, core.PatternOrderReplaced, domain.OrderEvent{Id: 10, Quantity: 10, FilledQuantity: 4}),
		orderStatusOutputEvent(t, core.PatternOrderOpen, domain.OrderEvent{Id: 20, Quantity: 10, FilledQuantity: 4}),
	}
	env := core.OutputEnvelope{InputSeq: 1, OutputSeq: 1, Events: events}
	tx := &orderStatusRecordingTx{}
	store := &orderStatusRecordingStore{tx: tx}
	projector := &DBProjector{store: store}

	if err := projector.applyBatch(context.Background(), 8, []core.OutputEnvelope{env}); err != nil {
		t.Fatalf("apply output envelope: %v", err)
	}

	want := []orderStatusUpdate{
		{orderID: 10, status: "REPLACED", quantity: 10, filledQty: 4},
		{orderID: 20, status: "OPEN", quantity: 10, filledQty: 4},
	}
	if len(tx.updates) != len(want) {
		t.Fatalf("status updates = %+v, want %+v", tx.updates, want)
	}
	for i := range want {
		if tx.updates[i] != want[i] {
			t.Errorf("status update[%d] = %+v, want %+v", i, tx.updates[i], want[i])
		}
	}
	if store.beginCount != 1 {
		t.Errorf("transaction begin count = %d, want 1", store.beginCount)
	}
	if !tx.committed {
		t.Error("transaction was not committed")
	}
	if tx.cursor != 8 {
		t.Errorf("saved DB cursor = %d, want 8", tx.cursor)
	}
}

func orderStatusOutputEvent(t *testing.T, pattern string, order domain.OrderEvent) core.OutputEvent {
	t.Helper()
	data, err := json.Marshal(order)
	if err != nil {
		t.Fatalf("marshal %s event: %v", pattern, err)
	}
	return core.OutputEvent{Pattern: pattern, Data: data}
}

type transferRecordingTx struct {
	Tx
	completed []completedTransfer
	rejected  []rejectedTransfer
}

type completedTransfer struct {
	transferID  int64
	completedAt time.Time
}

type rejectedTransfer struct {
	transferID  int64
	reason      string
	completedAt time.Time
}

func (t *transferRecordingTx) CompleteTransfer(_ context.Context, transferID int64, completedAt time.Time) error {
	t.completed = append(t.completed, completedTransfer{transferID, completedAt})
	return nil
}

func (t *transferRecordingTx) RejectTransfer(_ context.Context, transferID int64, reason string, completedAt time.Time) error {
	t.rejected = append(t.rejected, rejectedTransfer{transferID, reason, completedAt})
	return nil
}

func TestApplyEventRoutesTransferCompletedAndRejected(t *testing.T) {
	completedAt := time.Date(2026, 8, 25, 3, 4, 5, 0, time.UTC)
	tx := &transferRecordingTx{}

	completedEvent := transferOutputEvent(t, core.PatternTransferCompleted, domain.TransferCompleted{
		Id: 11, SenderAccountId: 1, RecipientAccountId: 2, Amount: 300, CompletedAt: completedAt,
	})
	if err := applyEvent(context.Background(), tx, completedEvent); err != nil {
		t.Fatalf("apply transfer completed: %v", err)
	}
	rejectedEvent := transferOutputEvent(t, core.PatternTransferRejected, domain.TransferRejected{
		Id: 12, SenderAccountId: 1, RecipientAccountId: 9, Amount: 400,
		Reason: string(core.TransferRejectInvalidRecipient), CompletedAt: completedAt,
	})
	if err := applyEvent(context.Background(), tx, rejectedEvent); err != nil {
		t.Fatalf("apply transfer rejected: %v", err)
	}

	if len(tx.completed) != 1 {
		t.Fatalf("completed transfers = %+v, want one", tx.completed)
	}
	if tx.completed[0].transferID != 11 {
		t.Errorf("completed transfer id = %d, want 11", tx.completed[0].transferID)
	}
	if !tx.completed[0].completedAt.Equal(completedAt) {
		t.Errorf("completed at = %s, want %s", tx.completed[0].completedAt, completedAt)
	}

	if len(tx.rejected) != 1 {
		t.Fatalf("rejected transfers = %+v, want one", tx.rejected)
	}
	if tx.rejected[0].transferID != 12 {
		t.Errorf("rejected transfer id = %d, want 12", tx.rejected[0].transferID)
	}
	if tx.rejected[0].reason != string(core.TransferRejectInvalidRecipient) {
		t.Errorf("reject reason = %s, want %s", tx.rejected[0].reason, core.TransferRejectInvalidRecipient)
	}
	if !tx.rejected[0].completedAt.Equal(completedAt) {
		t.Errorf("rejected completed at = %s, want %s", tx.rejected[0].completedAt, completedAt)
	}
}

func transferOutputEvent(t *testing.T, pattern string, data any) core.OutputEvent {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal transfer event: %v", err)
	}
	return core.OutputEvent{Pattern: pattern, Data: raw}
}
