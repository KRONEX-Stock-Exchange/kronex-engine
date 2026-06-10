package core

import (
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func ord(id int64, accountID int32, side domain.TradingType, price, qty uint64) domain.Order {
	return domain.Order{
		Id: id, AccountId: accountID, StockId: testStockID,
		Price: price, Quantity: qty, OrderType: domain.ORDER_LIMIT, TradingType: side,
	}
}

func acctOf(t *testing.T, e *Engine, id int32) domain.Account {
	t.Helper()
	a, ok := e.state.Accounts.Get(id)
	if !ok {
		t.Fatalf("account %d not found", id)
	}
	return a
}

func holdingOf(t *testing.T, e *Engine, accountID int32) domain.StockBalance {
	t.Helper()
	h, ok := e.state.StockBalances.Get(accountID, testStockID)
	if !ok {
		t.Fatalf("holding (acc=%d) not found", accountID)
	}
	return h
}

// [시나리오] 매수 100@70000 이 대기 매도 100 을 전량 체결
// [기대]    매수·매도 현금/보유 이동, 매수 평단=70000, 종목 현재가=70000(마지막 체결가)
func TestSettle_FullMatch(t *testing.T) {
	const buyer, seller int32 = 100, 200
	e := newMatchEngine(t)
	putAccount(e, buyer, 10_000_000)
	putAccount(e, seller, 0)
	putStock(e, testStockID, 60_000, domain.LISTED)
	// 매도자는 100주를 평단 50,000(총 5,000,000)에 보유.
	// 아래 대기 매도 100주가 호가창에 올라간 상태이므로 가용수량은 0으로 묶여 있다
	// (운영에서는 매도 주문 진입 시 AvailableQuantity 가 예약 차감됨).
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId: seller, StockId: testStockID,
		Quantity: 100, AvailableQuantity: 0, Average: 50_000, TotalBuyAmount: 5_000_000,
	})

	seed(t, e, ord(1, seller, domain.TRADING_SELL, 70_000, 100))
	if err := e.match(ord(2, buyer, domain.TRADING_BUY, 70_000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	// cash = 70,000 * 100 = 7,000,000
	b := acctOf(t, e, buyer)
	if b.Balance != 3_000_000 || b.AvailableBalance != 3_000_000 {
		t.Fatalf("buyer balance = (%d, %d), want (3000000, 3000000)", b.Balance, b.AvailableBalance)
	}
	s := acctOf(t, e, seller)
	if s.Balance != 7_000_000 || s.AvailableBalance != 7_000_000 {
		t.Fatalf("seller balance = (%d, %d), want (7000000, 7000000)", s.Balance, s.AvailableBalance)
	}

	bh := holdingOf(t, e, buyer)
	if bh.Quantity != 100 || bh.AvailableQuantity != 100 || bh.Average != 70_000 || bh.TotalBuyAmount != 7_000_000 {
		t.Fatalf("buyer holding = %+v, want Q100 avail100 avg70000 total7000000", bh)
	}
	sh := holdingOf(t, e, seller)
	if sh.Quantity != 0 || sh.AvailableQuantity != 0 || sh.Average != 0 || sh.TotalBuyAmount != 0 {
		t.Fatalf("seller holding = %+v, want all zero", sh)
	}

	if st, _ := e.state.Stocks.Get(testStockID); st.Price != 70_000 {
		t.Fatalf("stock price = %d, want 70000 (last trade)", st.Price)
	}
}

// [시나리오] 같은 계좌가 60000·70000 에 각각 100주씩 두 번 매수
// [기대]    보유 200주, 총매입 13,000,000, 가중평단 65,000
func TestSettle_BuyWeightedAverage(t *testing.T) {
	const buyer, s1, s2 int32 = 100, 201, 202
	e := newMatchEngine(t)
	putAccount(e, buyer, 100_000_000)
	putAccount(e, s1, 0)
	putAccount(e, s2, 0)
	putStock(e, testStockID, 60_000, domain.LISTED)
	putHolding(e, s1, testStockID, 100)
	putHolding(e, s2, testStockID, 100)

	// 1차: 100주 @ 60,000
	seed(t, e, ord(1, s1, domain.TRADING_SELL, 60_000, 100))
	if err := e.match(ord(2, buyer, domain.TRADING_BUY, 60_000, 100)); err != nil {
		t.Fatalf("match1: %v", err)
	}
	// 2차: 100주 @ 70,000
	seed(t, e, ord(3, s2, domain.TRADING_SELL, 70_000, 100))
	if err := e.match(ord(4, buyer, domain.TRADING_BUY, 70_000, 100)); err != nil {
		t.Fatalf("match2: %v", err)
	}

	bh := holdingOf(t, e, buyer)
	// 총 200주, 총매입 6,000,000 + 7,000,000 = 13,000,000, 평단 65,000
	if bh.Quantity != 200 || bh.TotalBuyAmount != 13_000_000 || bh.Average != 65_000 {
		t.Fatalf("buyer holding = %+v, want Q200 total13000000 avg65000", bh)
	}
}

// [시나리오] 평단 50000 으로 100주 보유한 계좌가 40주 매도(테이커)
// [기대]    잔량 60주·총매입 3,000,000·평단 50,000 유지, 매도대금 2,800,000 수령
func TestSettle_PartialSellKeepsAverage(t *testing.T) {
	const buyer, seller int32 = 100, 200
	e := newMatchEngine(t)
	putAccount(e, buyer, 10_000_000)
	putAccount(e, seller, 0)
	putStock(e, testStockID, 60_000, domain.LISTED)
	// 매도자 100주 @ 평단 50,000 (총 5,000,000)
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId: seller, StockId: testStockID,
		Quantity: 100, AvailableQuantity: 100, Average: 50_000, TotalBuyAmount: 5_000_000,
	})

	// 매수자가 70,000에 40주 대기 → 매도자가 시장에 40주 매도(테이커)
	seed(t, e, ord(1, buyer, domain.TRADING_BUY, 70_000, 40))
	if err := e.match(ord(2, seller, domain.TRADING_SELL, 70_000, 40)); err != nil {
		t.Fatalf("match: %v", err)
	}

	sh := holdingOf(t, e, seller)
	// 남은 60주, 총매입 5,000,000 - (50,000*40=2,000,000) = 3,000,000, 평단 50,000 유지
	if sh.Quantity != 60 || sh.TotalBuyAmount != 3_000_000 || sh.Average != 50_000 {
		t.Fatalf("seller holding = %+v, want Q60 total3000000 avg50000", sh)
	}
	// 매도 대금 = 70,000 * 40 = 2,800,000
	if s := acctOf(t, e, seller); s.Balance != 2_800_000 {
		t.Fatalf("seller balance = %d, want 2800000", s.Balance)
	}
	// 매수자는 40주 취득
	if bh := holdingOf(t, e, buyer); bh.Quantity != 40 || bh.Average != 70_000 {
		t.Fatalf("buyer holding = %+v, want Q40 avg70000", bh)
	}
}
