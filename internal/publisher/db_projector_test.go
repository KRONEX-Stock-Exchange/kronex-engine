// 테스트 항목:
// - 원주문 order.canceled 이벤트를 CANCELED 상태로 반영
// - 취소 요청 order.completed 이벤트를 COMPLETED 상태로 반영
// - 두 주문 상태와 DB 적용 커서를 하나의 트랜잭션에서 커밋
package publisher

import (
	"context"
	"encoding/json"
	"testing"

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
	orderID   int64
	status    string
	filledQty uint64
}

func (t *orderStatusRecordingTx) UpdateOrderStatus(_ context.Context, orderID int64, status string, filledQty uint64) error {
	t.updates = append(t.updates, orderStatusUpdate{orderID, status, filledQty})
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
		orderStatusOutputEvent(t, core.PatternOrderCanceled, domain.OrderEvent{OrderId: 10, FilledQuantity: 3}),
		orderStatusOutputEvent(t, core.PatternOrderCompleted, domain.OrderEvent{OrderId: 20}),
	}
	raw, err := json.Marshal(core.OutputEnvelope{InputSeq: 1, OutputSeq: 1, Events: events})
	if err != nil {
		t.Fatalf("marshal output envelope: %v", err)
	}
	tx := &orderStatusRecordingTx{}
	store := &orderStatusRecordingStore{tx: tx}
	projector := &DBProjector{store: store}

	if _, _, _, err := projector.apply(context.Background(), 7, raw); err != nil {
		t.Fatalf("apply output envelope: %v", err)
	}

	want := []orderStatusUpdate{
		{orderID: 10, status: "CANCELED", filledQty: 3},
		{orderID: 20, status: "COMPLETED", filledQty: 0},
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

func orderStatusOutputEvent(t *testing.T, pattern string, order domain.OrderEvent) core.OutputEvent {
	t.Helper()
	data, err := json.Marshal(order)
	if err != nil {
		t.Fatalf("marshal %s event: %v", pattern, err)
	}
	return core.OutputEvent{Pattern: pattern, Data: data}
}
