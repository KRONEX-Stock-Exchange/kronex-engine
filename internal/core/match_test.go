package core

import (
	"encoding/json"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

const testStockID int32 = 1

// match 만 돌리는 최소 Engine (output WAL + state). con/queue/input 은 match 가 안 씀.
func newMatchEngine(t *testing.T) *Engine {
	t.Helper()
	out, err := wal.Open(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open output wal: %v", err)
	}
	t.Cleanup(func() { _ = out.Close() })
	return &Engine{output: out, state: ledger.NewState()}
}

// 호가창에 대기 주문 미리 등록
func seed(t *testing.T, e *Engine, orders ...domain.Order) {
	t.Helper()
	ob := e.state.OrderBooks.Get(testStockID)
	for _, o := range orders {
		ob.Add(o)
	}
}

// output WAL 에 발행된 체결 내역 전부 읽기
func readTrades(t *testing.T, e *Engine) []domain.Trade {
	t.Helper()
	last, err := e.output.LastIndex()
	if err != nil {
		t.Fatalf("last index: %v", err)
	}
	var trades []domain.Trade
	for i := uint64(1); i <= last; i++ {
		data, err := e.output.Read(i)
		if err != nil {
			t.Fatalf("read wal %d: %v", i, err)
		}
		var tr domain.Trade
		if err := json.Unmarshal(data, &tr); err != nil {
			t.Fatalf("unmarshal trade %d: %v", i, err)
		}
		trades = append(trades, tr)
	}
	return trades
}

func limit(id int64, side domain.TradingType, price, qty uint64) domain.Order {
	return domain.Order{
		Id:          id,
		AccountId:   int32(id), // 기본은 주문마다 다른 계좌
		StockId:     testStockID,
		Price:       price,
		Quantity:    qty,
		OrderType:   domain.ORDER_LIMIT,
		TradingType: side,
	}
}

// 시장가 주문은 API 서버가 그날 상한가(매수)/하한가(매도)를 price 로 채워 보낸다.
// 따라서 헬퍼도 0 이 아닌 밴드 가격을 받는다.
func market(id int64, side domain.TradingType, price, qty uint64) domain.Order {
	o := limit(id, side, price, qty)
	o.OrderType = domain.ORDER_MARKET
	return o
}

// 호가창에 남아있는 주문의 잔량 조회
func remaining(t *testing.T, e *Engine, id int64) (qty uint64, resting bool) {
	t.Helper()
	o, ok := e.state.OrderBooks.Get(testStockID).Get(id)
	if !ok {
		return 0, false
	}
	return o.Quantity - o.FilledQuantity, true
}

// [시나리오] 대기 매도(100)에 더 작은 매수(30)가 들어옴
// [기대]    대기 30 체결·70 잔존, 테이커 전량 체결, 체결 1건(70000/30)
func TestMatch_PartialFillOfRestingOrder(t *testing.T) {
	e := newMatchEngine(t)
	// 대기: 매도 100 @ 70000
	seed(t, e, limit(1, domain.TRADING_SELL, 70000, 100))

	// 신규: 매수 30 @ 70000  → 대기 주문 30 만 체결
	if err := e.match(limit(2, domain.TRADING_BUY, 70000, 30)); err != nil {
		t.Fatalf("match: %v", err)
	}

	// 대기 매도는 70 남아야 함
	rem, resting := remaining(t, e, 1)
	if !resting || rem != 70 {
		t.Fatalf("resting sell remaining = (%d, %v), want (70, true)", rem, resting)
	}
	// 신규 매수는 전량 체결되어 호가창에 없어야 함
	if _, resting := remaining(t, e, 2); resting {
		t.Fatalf("taker buy should be fully filled, but it is resting")
	}
	// 체결 1건: 가격 70000(maker가), 수량 30, maker=1 taker=2
	trades := readTrades(t, e)
	if len(trades) != 1 {
		t.Fatalf("trades = %d, want 1", len(trades))
	}
	got := trades[0]
	if got.Price != 70000 || got.Quantity != 30 || got.MakerOrderId != 1 || got.TakerOrderId != 2 {
		t.Fatalf("trade = %+v, want price=70000 qty=30 maker=1 taker=2", got)
	}
}

// [시나리오] 대기 매도와 들어온 매수의 수량이 동일(100=100)
// [기대]    양쪽 전량 체결·호가창 완전히 비고, 체결 1건(qty=100)
func TestMatch_EqualQuantityFullMatch(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e, limit(1, domain.TRADING_SELL, 70000, 100))

	if err := e.match(limit(2, domain.TRADING_BUY, 70000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	// 둘 다 호가창에서 사라져야 함
	if _, resting := remaining(t, e, 1); resting {
		t.Fatalf("maker should be fully filled & removed")
	}
	if _, resting := remaining(t, e, 2); resting {
		t.Fatalf("taker should be fully filled & not rest")
	}
	// 호가창 완전히 비어야 함
	ob := e.state.OrderBooks.Get(testStockID)
	if _, ok := ob.BestAsk(); ok {
		t.Fatalf("book should have no asks")
	}
	if _, ok := ob.BestBid(); ok {
		t.Fatalf("book should have no bids")
	}
	trades := readTrades(t, e)
	if len(trades) != 1 || trades[0].Quantity != 100 {
		t.Fatalf("trades = %+v, want 1 trade qty=100", trades)
	}
}

// [시나리오] 들어온 매수(100)가 대기 매도(30)보다 큼
// [기대]    대기 전량 체결, 잔량 70 이 호가창에 등록(최우선 매수호가=70000)
func TestMatch_TakerLargerThanResting_RestsRemainder(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e, limit(1, domain.TRADING_SELL, 70000, 30))

	// 매수 100 @ 70000 → 30 체결, 70 잔량 등록
	if err := e.match(limit(2, domain.TRADING_BUY, 70000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	if _, resting := remaining(t, e, 1); resting {
		t.Fatalf("maker should be removed")
	}
	rem, resting := remaining(t, e, 2)
	if !resting || rem != 70 {
		t.Fatalf("taker remainder = (%d, %v), want (70, true)", rem, resting)
	}
	// 등록 후 최우선 매수호가 = 70000
	if p, ok := e.state.OrderBooks.Get(testStockID).BestBid(); !ok || p != 70000 {
		t.Fatalf("best bid = (%d, %v), want (70000, true)", p, ok)
	}
	if trades := readTrades(t, e); len(trades) != 1 || trades[0].Quantity != 30 {
		t.Fatalf("trades = %+v, want 1 trade qty=30", trades)
	}
}

// [시나리오] 매수 100@70100 이 두 매도 호가(70000,70100)를 쓸어담음
// [기대]    더 좋은 가격(70000)부터 50씩 체결, 체결 2건(70000→70100)
func TestMatch_SweepsMultipleLevels(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e,
		limit(1, domain.TRADING_SELL, 70000, 50),
		limit(2, domain.TRADING_SELL, 70100, 50),
	)

	// 매수 100 @ 70100 → 70000 50 + 70100 50
	if err := e.match(limit(3, domain.TRADING_BUY, 70100, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	trades := readTrades(t, e)
	if len(trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(trades))
	}
	// 더 좋은 가격(낮은 매도가)부터 체결
	if trades[0].Price != 70000 || trades[0].Quantity != 50 {
		t.Fatalf("trade[0] = %+v, want price=70000 qty=50", trades[0])
	}
	if trades[1].Price != 70100 || trades[1].Quantity != 50 {
		t.Fatalf("trade[1] = %+v, want price=70100 qty=50", trades[1])
	}
}

// [시나리오] 같은 가격(70000)에 매도 2건, 매수 40 이 들어옴
// [기대]    먼저 들어온 id1(30) 전량 후 id2(10) 부분 체결 — FIFO 순서
func TestMatch_SamePriceFIFO(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e,
		limit(1, domain.TRADING_SELL, 70000, 30), // 먼저
		limit(2, domain.TRADING_SELL, 70000, 30), // 나중
	)

	// 매수 40 → id1 전량(30) + id2 일부(10)
	if err := e.match(limit(3, domain.TRADING_BUY, 70000, 40)); err != nil {
		t.Fatalf("match: %v", err)
	}

	if _, resting := remaining(t, e, 1); resting {
		t.Fatalf("id1 should be filled first and removed")
	}
	rem, resting := remaining(t, e, 2)
	if !resting || rem != 20 {
		t.Fatalf("id2 remaining = (%d, %v), want (20, true)", rem, resting)
	}
	trades := readTrades(t, e)
	if len(trades) != 2 ||
		trades[0].MakerOrderId != 1 || trades[0].Quantity != 30 ||
		trades[1].MakerOrderId != 2 || trades[1].Quantity != 10 {
		t.Fatalf("trades = %+v, want maker1/qty30 then maker2/qty10", trades)
	}
}

// [시나리오] 매수 50@70000 이 최우선 매도 70100 보다 낮아 교차 안 함
// [기대]    체결 0건, 매도는 그대로·매수는 전량 호가창에 등록
func TestMatch_LimitPriceNotCrossing_RestsNoTrade(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e, limit(1, domain.TRADING_SELL, 70100, 30))

	// 매수 50 @ 70000 (최우선 매도 70100 보다 낮음) → 체결 X
	if err := e.match(limit(2, domain.TRADING_BUY, 70000, 50)); err != nil {
		t.Fatalf("match: %v", err)
	}

	if rem, resting := remaining(t, e, 1); !resting || rem != 30 {
		t.Fatalf("resting sell should be untouched, got (%d, %v)", rem, resting)
	}
	if rem, resting := remaining(t, e, 2); !resting || rem != 50 {
		t.Fatalf("buy should rest fully, got (%d, %v)", rem, resting)
	}
	if trades := readTrades(t, e); len(trades) != 0 {
		t.Fatalf("trades = %d, want 0", len(trades))
	}
}

// [시나리오] 시장가 매수(100)에 매도 유동성이 30 뿐
// [기대]    가능한 30 만 체결, 잔량은 호가창에 등록되지 않음(시장가 미등록)
func TestMatch_MarketOrder_PartialFill_LeftoverDropped(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e, limit(1, domain.TRADING_SELL, 70000, 30))

	if err := e.match(market(2, domain.TRADING_BUY, 100_000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	if _, resting := remaining(t, e, 1); resting {
		t.Fatalf("maker should be filled")
	}
	// 시장가는 잔량이 남아도 호가창에 등록되면 안 됨
	if _, resting := remaining(t, e, 2); resting {
		t.Fatalf("market order must not rest in the book")
	}
	if p, ok := e.state.OrderBooks.Get(testStockID).BestBid(); ok {
		t.Fatalf("book should have no bids, got best bid %d", p)
	}
	if trades := readTrades(t, e); len(trades) != 1 || trades[0].Quantity != 30 {
		t.Fatalf("trades = %+v, want 1 trade qty=30", trades)
	}
}

// [시나리오] 빈 호가창에 지정가 매수(50)가 들어옴
// [기대]    체결 0건, 주문 전량 그대로 호가창에 등록
func TestMatch_EmptyBook_RestsNoTrade(t *testing.T) {
	e := newMatchEngine(t)

	if err := e.match(limit(1, domain.TRADING_BUY, 70000, 50)); err != nil {
		t.Fatalf("match: %v", err)
	}
	if rem, resting := remaining(t, e, 1); !resting || rem != 50 {
		t.Fatalf("order should rest fully, got (%d, %v)", rem, resting)
	}
	if trades := readTrades(t, e); len(trades) != 0 {
		t.Fatalf("trades = %d, want 0", len(trades))
	}
}

// [시나리오] 같은 계좌(777)의 매도·매수가 서로 만남 (자전거래)
// [기대]    현재는 방지 로직이 없어 체결 1건 발생 — 미구현 갭 고정(정책 도입 시 수정)
func TestMatch_SelfTrade_NotPreventedYet(t *testing.T) {
	e := newMatchEngine(t)
	maker := limit(1, domain.TRADING_SELL, 70000, 100)
	maker.AccountId = 777
	seed(t, e, maker)

	taker := limit(2, domain.TRADING_BUY, 70000, 100)
	taker.AccountId = 777 // 같은 계좌
	if err := e.match(taker); err != nil {
		t.Fatalf("match: %v", err)
	}

	trades := readTrades(t, e)
	if len(trades) != 1 {
		t.Fatalf("현재는 자전거래가 막히지 않아 체결 1건이 나야 함 (got %d). "+
			"방지 정책 도입 시 이 테스트를 수정할 것", len(trades))
	}
}
