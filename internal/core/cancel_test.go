// 테스트 항목:
// - 부분 체결된 매수 주문 취소 시 미체결 예약금 복구와 이벤트 순서
// - 부분 체결된 매도 주문 취소 시 미체결 가용수량 복구와 이벤트 순서
// - 같은 가격대의 다른 주문을 유지한 최종 호가 잔량
// - 잘못된 취소 요청 FilledQuantity 검증
// - 미존재·전량 체결 주문 취소 시 ORDER_NOT_ACTIVE 거부와 자산 불변
// - 같은 원주문에 대한 두 번째 취소 거부와 이중 환불 방지
package core

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

func TestCancelBuyOrderRestoresRemainingBalanceAndAppendsStatusEvents(t *testing.T) {
	e, output := newCancelTestEngine(t)
	e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 600, AvailableBalance: 0})

	target := domain.Order{
		Id: 10, AccountId: 1, StockId: 1, Price: 100,
		Quantity: 10, FilledQuantity: 4,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}
	ob := e.state.OrderBooks.Get(target.StockId)
	ob.Add(target)
	ob.Add(domain.Order{
		Id: 12, AccountId: 2, StockId: 1, Price: 100, Quantity: 2,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	})
	cancelRequest := domain.Order{
		Id: 20, TargetId: target.Id, AccountId: target.AccountId,
		StockId: target.StockId, TradingType: domain.TRADING_CANCEL,
	}

	if err := e.cancel(cancelRequest); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, ok := e.state.OrderBooks.Get(target.StockId).Get(target.Id); ok {
		t.Fatal("target order remains in order book after cancel")
	}
	account, ok := e.state.Accounts.Get(target.AccountId)
	if !ok {
		t.Fatal("account not found after cancel")
	}
	if account.Balance != 600 {
		t.Errorf("balance = %d, want 600", account.Balance)
	}
	if account.AvailableBalance != 600 {
		t.Errorf("available balance = %d, want 600", account.AvailableBalance)
	}

	env := readCancelOutput(t, output)
	assertEventPatterns(t, env,
		PatternOrderCanceled,
		PatternOrderCompleted,
		PatternOrderBookUpdated,
		PatternAccountUpdated,
	)
	assertCancelStatusEvents(t, env, target, cancelRequest)
	assertOrderBookLevelEvent(t, env, "BUY", target.Price, 2)
}

func TestCancelSellOrderRestoresRemainingQuantityAndAppendsStatusEvents(t *testing.T) {
	e, output := newCancelTestEngine(t)
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId: 1, StockId: 1, Quantity: 7, AvailableQuantity: 0,
		Average: 100, TotalBuyAmount: 700,
	})

	target := domain.Order{
		Id: 11, AccountId: 1, StockId: 1, Price: 100,
		Quantity: 10, FilledQuantity: 3,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_SELL,
	}
	e.state.OrderBooks.Get(target.StockId).Add(target)
	cancelRequest := domain.Order{
		Id: 21, TargetId: target.Id, AccountId: target.AccountId,
		StockId: target.StockId, TradingType: domain.TRADING_CANCEL,
	}

	if err := e.cancel(cancelRequest); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	holding, ok := e.state.StockBalances.Get(target.AccountId, target.StockId)
	if !ok {
		t.Fatal("holding not found after cancel")
	}
	if holding.Quantity != 7 {
		t.Errorf("quantity = %d, want 7", holding.Quantity)
	}
	if holding.AvailableQuantity != 7 {
		t.Errorf("available quantity = %d, want 7", holding.AvailableQuantity)
	}

	env := readCancelOutput(t, output)
	assertEventPatterns(t, env,
		PatternOrderCanceled,
		PatternOrderCompleted,
		PatternOrderBookUpdated,
		PatternHoldingUpdated,
	)
	assertCancelStatusEvents(t, env, target, cancelRequest)
	assertOrderBookLevelEvent(t, env, "SELL", target.Price, 0)
}

func TestValidateCancelRequestRejectsFilledQuantity(t *testing.T) {
	e := &Engine{state: ledger.NewState()}
	err := e.validateOrder(domain.Order{
		Id: 20, TargetId: 10, FilledQuantity: 1,
		TradingType: domain.TRADING_CANCEL,
	})
	if err == nil {
		t.Fatal("validateOrder accepted cancel request with filled quantity")
	}
	if got := rejectReasonOf(err); got != RejectInvalidOrder {
		t.Errorf("reject reason = %s, want %s", got, RejectInvalidOrder)
	}
}

func TestHandleCancelRejectsInactiveOrderWithoutChangingBalance(t *testing.T) {
	tests := []struct {
		name string
		seed func(*Engine)
	}{
		{name: "missing"},
		{
			name: "filled",
			seed: func(e *Engine) {
				ob := e.state.OrderBooks.Get(1)
				ob.Add(domain.Order{
					Id: 10, AccountId: 1, StockId: 1, Price: 100, Quantity: 1,
					OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
				})
				ob.Fill(10, 1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e, output := newCancelHandleTestEngine(t)
			e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 1_000, AvailableBalance: 1_000})
			if test.seed != nil {
				test.seed(e)
			}

			request := domain.Order{
				Id: 20, TargetId: 10, AccountId: 1, StockId: 1,
				OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_CANCEL,
			}
			if err := e.handle(cancelTestDelivery(t, request)); err != nil {
				t.Fatalf("handle cancel: %v", err)
			}

			account, ok := e.state.Accounts.Get(1)
			if !ok {
				t.Fatal("account not found after rejected cancel")
			}
			if account.Balance != 1_000 || account.AvailableBalance != 1_000 {
				t.Errorf("account changed after rejected cancel: %+v", account)
			}
			assertRejectedCancelEvent(t, readOutputAt(t, output, 1), request.Id)
		})
	}
}

func TestHandleSecondCancelIsRejectedWithoutDoubleRefund(t *testing.T) {
	e, output := newCancelHandleTestEngine(t)
	e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 600, AvailableBalance: 0})
	target := domain.Order{
		Id: 10, AccountId: 1, StockId: 1, Price: 100,
		Quantity: 10, FilledQuantity: 4,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}
	e.state.OrderBooks.Get(1).Add(target)

	first := domain.Order{
		Id: 20, TargetId: target.Id, AccountId: 1, StockId: 1,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_CANCEL,
	}
	if err := e.handle(cancelTestDelivery(t, first)); err != nil {
		t.Fatalf("handle first cancel: %v", err)
	}
	account, _ := e.state.Accounts.Get(1)
	if account.AvailableBalance != 600 {
		t.Fatalf("available balance after first cancel = %d, want 600", account.AvailableBalance)
	}

	second := first
	second.Id = 21
	if err := e.handle(cancelTestDelivery(t, second)); err != nil {
		t.Fatalf("handle second cancel: %v", err)
	}
	account, _ = e.state.Accounts.Get(1)
	if account.AvailableBalance != 600 {
		t.Errorf("available balance after second cancel = %d, want 600", account.AvailableBalance)
	}

	assertCancelStatusEvents(t, readOutputAt(t, output, 1), target, first)
	assertRejectedCancelEvent(t, readOutputAt(t, output, 2), second.Id)
}

func newCancelTestEngine(t *testing.T) (*Engine, *wal.WAL) {
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

func newCancelHandleTestEngine(t *testing.T) (*Engine, *wal.WAL) {
	t.Helper()
	dir := t.TempDir()
	input, err := wal.Open(filepath.Join(dir, "input"), nil)
	if err != nil {
		t.Fatalf("open input WAL: %v", err)
	}
	output, err := wal.Open(filepath.Join(dir, "output"), nil)
	if err != nil {
		_ = input.Close()
		t.Fatalf("open output WAL: %v", err)
	}

	e := &Engine{
		input:        input,
		output:       output,
		state:        ledger.NewState(),
		dedup:        newDedup(dedupWindow),
		outputSignal: make(chan struct{}, 1),
	}
	e.routes = map[string]func(Delivery) error{"cancel_data": e.handleData}
	t.Cleanup(func() { _ = e.Close() })
	return e, output
}

func cancelTestDelivery(t *testing.T, order domain.Order) Delivery {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"id":             strconv.FormatInt(order.Id, 10),
		"targetId":       strconv.FormatInt(order.TargetId, 10),
		"accountId":      strconv.FormatInt(int64(order.AccountId), 10),
		"stockId":        strconv.FormatInt(int64(order.StockId), 10),
		"price":          strconv.FormatUint(order.Price, 10),
		"quantity":       strconv.FormatUint(order.Quantity, 10),
		"filledQuantity": strconv.FormatUint(order.FilledQuantity, 10),
		"orderType":      "LIMIT",
		"tradingType":    "CANCEL",
	})
	if err != nil {
		t.Fatalf("marshal cancel request: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"pattern": PatternOrderCreated,
		"data":    json.RawMessage(data),
	})
	if err != nil {
		t.Fatalf("marshal cancel envelope: %v", err)
	}
	return Delivery{
		Queue: "cancel_data",
		Message: domain.Message{
			RoutingKey: "cancel_data",
			Payload:    payload,
		},
		Ack:  func() error { return nil },
		Nack: func(bool) error { return nil },
	}
}

func readCancelOutput(t *testing.T, output *wal.WAL) OutputEnvelope {
	t.Helper()
	return readOutputAt(t, output, 1)
}

func readOutputAt(t *testing.T, output *wal.WAL, index uint64) OutputEnvelope {
	t.Helper()
	raw, err := output.Read(index)
	if err != nil {
		t.Fatalf("read output WAL: %v", err)
	}
	var env OutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal output envelope: %v", err)
	}
	return env
}

func assertRejectedCancelEvent(t *testing.T, env OutputEnvelope, requestID int64) {
	t.Helper()
	if len(env.Events) != 1 {
		t.Fatalf("rejected cancel events = %+v, want one event", env.Events)
	}
	event := env.Events[0]
	if event.Pattern != PatternOrderRejected {
		t.Fatalf("rejected cancel pattern = %s, want %s", event.Pattern, PatternOrderRejected)
	}
	var rejected domain.OrderRejected
	if err := json.Unmarshal(event.Data, &rejected); err != nil {
		t.Fatalf("unmarshal rejected cancel: %v", err)
	}
	if rejected.OrderId != requestID {
		t.Errorf("rejected order ID = %d, want %d", rejected.OrderId, requestID)
	}
	if rejected.Reason != string(RejectOrderNotActive) {
		t.Errorf("reject reason = %s, want %s", rejected.Reason, RejectOrderNotActive)
	}
}

func assertEventPatterns(t *testing.T, env OutputEnvelope, want ...string) {
	t.Helper()
	if len(env.Events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(env.Events), len(want), env.Events)
	}
	for i, pattern := range want {
		if env.Events[i].Pattern != pattern {
			t.Errorf("event[%d] pattern = %s, want %s", i, env.Events[i].Pattern, pattern)
		}
	}
}

func assertCancelStatusEvents(t *testing.T, env OutputEnvelope, target, cancelRequest domain.Order) {
	t.Helper()
	statusIDs := make(map[string]int64)
	var statusPatterns []string
	var canceled domain.OrderEvent
	for _, event := range env.Events {
		if event.Pattern != PatternOrderCanceled && event.Pattern != PatternOrderCompleted {
			continue
		}
		statusPatterns = append(statusPatterns, event.Pattern)
		var order domain.OrderEvent
		if err := json.Unmarshal(event.Data, &order); err != nil {
			t.Fatalf("unmarshal %s event: %v", event.Pattern, err)
		}
		statusIDs[event.Pattern] = order.OrderId
		if event.Pattern == PatternOrderCanceled {
			canceled = order
		}
	}

	if len(statusPatterns) != 2 || statusPatterns[0] != PatternOrderCanceled || statusPatterns[1] != PatternOrderCompleted {
		t.Errorf("status event patterns = %v, want [%s %s]", statusPatterns, PatternOrderCanceled, PatternOrderCompleted)
	}
	if got := statusIDs[PatternOrderCanceled]; got != target.Id {
		t.Errorf("canceled order ID = %d, want %d", got, target.Id)
	}
	if got := statusIDs[PatternOrderCompleted]; got != cancelRequest.Id {
		t.Errorf("completed order ID = %d, want %d", got, cancelRequest.Id)
	}
	if canceled.FilledQuantity != target.FilledQuantity {
		t.Errorf("canceled filled quantity = %d, want %d", canceled.FilledQuantity, target.FilledQuantity)
	}
}

func assertOrderBookLevelEvent(t *testing.T, env OutputEnvelope, side string, price, quantity uint64) {
	t.Helper()
	for _, event := range env.Events {
		if event.Pattern != PatternOrderBookUpdated {
			continue
		}
		var update domain.OrderBookUpdated
		if err := json.Unmarshal(event.Data, &update); err != nil {
			t.Fatalf("unmarshal orderbook event: %v", err)
		}
		if len(update.Levels) != 1 {
			t.Fatalf("orderbook levels = %+v, want one level", update.Levels)
		}
		level := update.Levels[0]
		if level.Side != side || level.Price != price || level.Quantity != quantity {
			t.Errorf("orderbook level = %+v, want side=%s price=%d quantity=%d", level, side, price, quantity)
		}
		return
	}
	t.Fatal("orderbook.updated event not found")
}
