// 테스트 항목:
// - 부분 체결된 매수 주문 가격 정정 시 누적 체결량·잔량·예약금 승계
// - 부분 체결된 매도 주문 가격 정정 시 누적 체결량·잔량·예약수량 승계
// - 정정 직후 부분·전량 체결 시 누적 체결량, OPEN/FILLED 상태와 taker 매수 방향
// - 기존·신규 가격대 최종 잔량과 신규 가격대 FIFO 후순위
// - 수량 변경·동일 가격·비활성 대상 정정 거부
// - 잔액 부족 정정 실패 시 원주문·자산·호가 원자성
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

func TestEditPartiallyFilledBuyCarriesProgressAndRepricesRemaining(t *testing.T) {
	e, output := newEditHandleTestEngine(t)
	seedListedStock(e, 1)
	e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 600, AvailableBalance: 0})
	target := domain.Order{
		Id: 10, AccountId: 1, StockId: 1, Price: 100,
		Quantity: 10, FilledQuantity: 4,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}
	e.state.OrderBooks.Get(1).Add(target)
	request := editRequest(20, target, 90)
	request.FilledQuantity = 9

	if err := e.handle(editTestDelivery(t, request)); err != nil {
		t.Fatalf("handle edit: %v", err)
	}

	ob := e.state.OrderBooks.Get(1)
	if _, ok := ob.Get(target.Id); ok {
		t.Fatal("target order remains after edit")
	}
	replacement, ok := ob.Get(request.Id)
	if !ok {
		t.Fatal("replacement order not found")
	}
	assertReplacementOrder(t, replacement, request, target, 4)
	if got := ob.LevelQuantity(domain.TRADING_BUY, request.Price); got != 6 {
		t.Errorf("replacement level quantity = %d, want 6", got)
	}
	account, _ := e.state.Accounts.Get(1)
	if account.Balance != 600 || account.AvailableBalance != 60 {
		t.Errorf("account after edit = %+v, want balance=600 available=60", account)
	}

	env := readOutputAt(t, output, 1)
	assertEventPatterns(t, env,
		PatternOrderReplaced,
		PatternOrderOpen,
		PatternOrderBookUpdated,
		PatternAccountUpdated,
	)
	assertEditOrderEvent(t, env, PatternOrderReplaced, target.Id, target.Quantity, target.FilledQuantity)
	assertEditOrderEvent(t, env, PatternOrderOpen, request.Id, target.Quantity, target.FilledQuantity)
	assertEditOrderBookLevel(t, env, "BUY", target.Price, 0)
	assertEditOrderBookLevel(t, env, "BUY", request.Price, 6)
	assertSingleOutputEnvelope(t, output)
}

func TestEditPartiallyFilledSellCarriesProgressAndReservation(t *testing.T) {
	e, output := newEditHandleTestEngine(t)
	seedListedStock(e, 1)
	e.state.Accounts.Upsert(&domain.Account{Id: 1})
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId: 1, StockId: 1, Quantity: 7, AvailableQuantity: 0,
		Average: 100, TotalBuyAmount: 700,
	})
	target := domain.Order{
		Id: 10, AccountId: 1, StockId: 1, Price: 100,
		Quantity: 10, FilledQuantity: 3,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_SELL,
	}
	e.state.OrderBooks.Get(1).Add(target)
	request := editRequest(20, target, 110)

	if err := e.handle(editTestDelivery(t, request)); err != nil {
		t.Fatalf("handle edit: %v", err)
	}

	replacement, ok := e.state.OrderBooks.Get(1).Get(request.Id)
	if !ok {
		t.Fatal("replacement order not found")
	}
	assertReplacementOrder(t, replacement, request, target, 3)
	holding, _ := e.state.StockBalances.Get(1, 1)
	if holding.Quantity != 7 || holding.AvailableQuantity != 0 {
		t.Errorf("holding after edit = %+v, want quantity=7 available=0", holding)
	}
	env := readOutputAt(t, output, 1)
	assertEditOrderBookLevel(t, env, "SELL", target.Price, 0)
	assertEditOrderBookLevel(t, env, "SELL", request.Price, 7)
}

func TestEditImmediatelyMatchesWithCumulativeFilledQuantity(t *testing.T) {
	tests := []struct {
		name              string
		counterQuantity   uint64
		wantFilled        uint64
		wantBalance       uint64
		wantAvailable     uint64
		wantStatusPattern string
		wantResting       bool
	}{
		{
			name: "partial", counterQuantity: 2, wantFilled: 6,
			wantBalance: 410, wantAvailable: 10,
			wantStatusPattern: PatternOrderOpen, wantResting: true,
		},
		{
			name: "full", counterQuantity: 6, wantFilled: 10,
			wantBalance: 30, wantAvailable: 30,
			wantStatusPattern: PatternOrderFilled, wantResting: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e, output := newEditHandleTestEngine(t)
			seedListedStock(e, 1)
			e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 600, AvailableBalance: 60})
			e.state.Accounts.Upsert(&domain.Account{Id: 2})
			e.state.StockBalances.Upsert(&domain.StockBalance{
				AccountId: 2, StockId: 1,
				Quantity: test.counterQuantity, AvailableQuantity: 0,
				Average: 1, TotalBuyAmount: test.counterQuantity,
			})
			target := domain.Order{
				Id: 10, AccountId: 1, StockId: 1, Price: 90,
				Quantity: 10, FilledQuantity: 4,
				OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
			}
			counter := domain.Order{
				Id: 30, AccountId: 2, StockId: 1, Price: 95,
				Quantity:  test.counterQuantity,
				OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_SELL,
			}
			ob := e.state.OrderBooks.Get(1)
			ob.Add(target)
			ob.Add(counter)
			request := editRequest(20, target, 100)

			if err := e.handle(editTestDelivery(t, request)); err != nil {
				t.Fatalf("handle edit: %v", err)
			}

			replacement, resting := ob.Get(request.Id)
			if resting != test.wantResting {
				t.Fatalf("replacement resting = %t, want %t", resting, test.wantResting)
			}
			if resting {
				assertReplacementOrder(t, replacement, request, target, test.wantFilled)
			}
			account, _ := e.state.Accounts.Get(1)
			if account.Balance != test.wantBalance || account.AvailableBalance != test.wantAvailable {
				t.Errorf("buyer account = %+v, want balance=%d available=%d", account, test.wantBalance, test.wantAvailable)
			}

			env := readOutputAt(t, output, 1)
			assertEditOrderEvent(t, env, test.wantStatusPattern, request.Id, target.Quantity, test.wantFilled)
			trade := findEditTrade(t, env)
			if trade.TakerOrderId != request.Id || trade.Quantity != test.counterQuantity || trade.TradingType != domain.TRADING_BUY {
				t.Errorf("trade = %+v, want taker=%d quantity=%d tradingType=BUY", trade, request.Id, test.counterQuantity)
			}
			assertSingleOutputEnvelope(t, output)
		})
	}
}

func TestEditMovesReplacementToBackOfNewPriceLevel(t *testing.T) {
	e, _ := newEditHandleTestEngine(t)
	seedListedStock(e, 1)
	e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 600, AvailableBalance: 0})
	target := domain.Order{
		Id: 10, AccountId: 1, StockId: 1, Price: 100,
		Quantity: 10, FilledQuantity: 4,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}
	ahead := domain.Order{
		Id: 30, AccountId: 2, StockId: 1, Price: 90, Quantity: 3,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}
	ob := e.state.OrderBooks.Get(1)
	ob.Add(target)
	ob.Add(ahead)
	request := editRequest(20, target, 90)

	if err := e.handle(editTestDelivery(t, request)); err != nil {
		t.Fatalf("handle edit: %v", err)
	}
	front, ok := ob.Front(domain.TRADING_BUY, 90)
	if !ok || front.Id != ahead.Id {
		t.Errorf("front order = %+v, want existing order %d", front, ahead.Id)
	}
	if got := ob.LevelQuantity(domain.TRADING_BUY, 90); got != 9 {
		t.Errorf("new price level quantity = %d, want 9", got)
	}
}

func TestValidateEditRejectsNonPriceChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.Order)
		want   RejectReason
	}{
		{name: "zero price", mutate: func(o *domain.Order) { o.Price = 0 }, want: RejectInvalidOrder},
		{name: "same price", mutate: func(o *domain.Order) { o.Price = 100 }, want: RejectInvalidOrder},
		{name: "quantity", mutate: func(o *domain.Order) { o.Quantity = 1 }, want: RejectInvalidOrder},
		{name: "same id", mutate: func(o *domain.Order) { o.Id = o.TargetId }, want: RejectInvalidOrder},
		{name: "wrong account", mutate: func(o *domain.Order) { o.AccountId = 2 }, want: RejectOrderNotActive},
		{name: "missing target", mutate: func(o *domain.Order) { o.TargetId = 999 }, want: RejectOrderNotActive},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := &Engine{state: ledger.NewState()}
			seedListedStock(e, 1)
			e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 1_000, AvailableBalance: 400})
			e.state.Accounts.Upsert(&domain.Account{Id: 2})
			target := domain.Order{
				Id: 10, AccountId: 1, StockId: 1, Price: 100,
				Quantity: 10, FilledQuantity: 4,
				OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
			}
			e.state.OrderBooks.Get(1).Add(target)
			request := editRequest(20, target, 90)
			test.mutate(&request)

			err := e.validateOrder(request)
			if err == nil {
				t.Fatal("validateOrder accepted invalid edit")
			}
			if got := rejectReasonOf(err); got != test.want {
				t.Errorf("reject reason = %s, want %s", got, test.want)
			}
			if current, ok := e.state.OrderBooks.Get(1).Get(target.Id); !ok || current != target {
				t.Errorf("target changed during validation: %+v, exists=%t", current, ok)
			}
		})
	}
}

func TestEditBuyRejectsInsufficientBalanceAtomically(t *testing.T) {
	e, output := newEditHandleTestEngine(t)
	seedListedStock(e, 1)
	e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 600, AvailableBalance: 0})
	target := domain.Order{
		Id: 10, AccountId: 1, StockId: 1, Price: 100,
		Quantity: 10, FilledQuantity: 4,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}
	e.state.OrderBooks.Get(1).Add(target)
	request := editRequest(20, target, 110)

	if err := e.handle(editTestDelivery(t, request)); err != nil {
		t.Fatalf("handle edit: %v", err)
	}

	current, ok := e.state.OrderBooks.Get(1).Get(target.Id)
	if !ok || current != target {
		t.Errorf("target after rejected edit = %+v, exists=%t", current, ok)
	}
	if _, ok := e.state.OrderBooks.Get(1).Get(request.Id); ok {
		t.Fatal("replacement exists after rejected edit")
	}
	account, _ := e.state.Accounts.Get(1)
	if account.Balance != 600 || account.AvailableBalance != 0 {
		t.Errorf("account changed after rejected edit: %+v", account)
	}
	assertRejectedEditEvent(t, readOutputAt(t, output, 1), request.Id, RejectInsufficientBalance)
}

func newEditHandleTestEngine(t *testing.T) (*Engine, *wal.WAL) {
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
	e.routes = map[string]func(Delivery) error{"edit_data": e.handleData}
	t.Cleanup(func() { _ = e.Close() })
	return e, output
}

func seedListedStock(e *Engine, stockID int32) {
	e.state.Stocks.Upsert(&domain.Stock{Id: stockID, Price: 100, Status: domain.LISTED})
}

func editRequest(id int64, target domain.Order, price uint64) domain.Order {
	return domain.Order{
		Id: id, TargetId: target.Id, AccountId: target.AccountId, StockId: target.StockId,
		Price: price, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_EDIT,
	}
}

func editTestDelivery(t *testing.T, order domain.Order) Delivery {
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
		"tradingType":    "EDIT",
	})
	if err != nil {
		t.Fatalf("marshal edit request: %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"pattern": PatternOrderCreated,
		"data":    json.RawMessage(data),
	})
	if err != nil {
		t.Fatalf("marshal edit envelope: %v", err)
	}
	return Delivery{
		Queue:   "edit_data",
		Message: domain.Message{RoutingKey: "edit_data", Payload: payload},
		Ack:     func() error { return nil },
		Nack:    func(bool) error { return nil },
	}
}

func assertReplacementOrder(t *testing.T, got, request, target domain.Order, wantFilled uint64) {
	t.Helper()
	if got.Id != request.Id || got.TargetId != target.Id || got.Price != request.Price ||
		got.Quantity != target.Quantity || got.FilledQuantity != wantFilled ||
		got.OrderType != target.OrderType || got.TradingType != target.TradingType {
		t.Errorf("replacement = %+v, want id=%d target=%d price=%d quantity=%d filled=%d type=%d side=%d",
			got, request.Id, target.Id, request.Price, target.Quantity, wantFilled, target.OrderType, target.TradingType)
	}
}

func assertEditOrderEvent(t *testing.T, env OutputEnvelope, pattern string, orderID int64, quantity, filled uint64) {
	t.Helper()
	for _, event := range env.Events {
		if event.Pattern != pattern {
			continue
		}
		var order domain.OrderEvent
		if err := json.Unmarshal(event.Data, &order); err != nil {
			t.Fatalf("unmarshal %s: %v", pattern, err)
		}
		if order.Id == orderID {
			if order.Quantity != quantity || order.FilledQuantity != filled {
				t.Errorf("%s order = %+v, want quantity=%d filled=%d", pattern, order, quantity, filled)
			}
			return
		}
	}
	t.Fatalf("%s event for order %d not found", pattern, orderID)
}

func assertEditOrderBookLevel(t *testing.T, env OutputEnvelope, side string, price, quantity uint64) {
	t.Helper()
	for _, event := range env.Events {
		if event.Pattern != PatternOrderBookUpdated {
			continue
		}
		var update domain.OrderBookUpdated
		if err := json.Unmarshal(event.Data, &update); err != nil {
			t.Fatalf("unmarshal orderbook update: %v", err)
		}
		for _, level := range update.Levels {
			if level.Side == side && level.Price == price {
				if level.Quantity != quantity {
					t.Errorf("level %s %d quantity = %d, want %d", side, price, level.Quantity, quantity)
				}
				return
			}
		}
	}
	t.Fatalf("orderbook level %s %d not found", side, price)
}

func findEditTrade(t *testing.T, env OutputEnvelope) domain.Trade {
	t.Helper()
	for _, event := range env.Events {
		if event.Pattern != PatternTradeExecuted {
			continue
		}
		var trade domain.Trade
		if err := json.Unmarshal(event.Data, &trade); err != nil {
			t.Fatalf("unmarshal trade: %v", err)
		}
		return trade
	}
	t.Fatal("trade.executed event not found")
	return domain.Trade{}
}

func assertSingleOutputEnvelope(t *testing.T, output *wal.WAL) {
	t.Helper()
	last, err := output.LastIndex()
	if err != nil {
		t.Fatalf("output last index: %v", err)
	}
	if last != 1 {
		t.Errorf("output WAL records = %d, want 1", last)
	}
}

func assertRejectedEditEvent(t *testing.T, env OutputEnvelope, orderID int64, reason RejectReason) {
	t.Helper()
	if len(env.Events) != 1 || env.Events[0].Pattern != PatternOrderRejected {
		t.Fatalf("rejected edit events = %+v, want one %s", env.Events, PatternOrderRejected)
	}
	var rejected domain.OrderRejected
	if err := json.Unmarshal(env.Events[0].Data, &rejected); err != nil {
		t.Fatalf("unmarshal rejected edit: %v", err)
	}
	if rejected.OrderId != orderID || rejected.Reason != string(reason) {
		t.Errorf("rejected edit = %+v, want id=%d reason=%s", rejected, orderID, reason)
	}
}
