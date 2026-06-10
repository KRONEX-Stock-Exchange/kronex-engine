package core

import (
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// 이 파일은 "체결 로직 경우의 수" 전수 검증용 테스트를 모은다.
//  - 매칭 갭(A7/A9/B2/B3): 올바른 동작을 고정한다(통과 기대).
//  - 정산 버그(C4/C5): 올바른 값을 단언한다(현재 구현이면 실패 → 버그 입증).

// [시나리오] 매도 100@69900 이 두 매수 호가(70000,69900)를 쓸어담음 (A7)
// [기대]    더 좋은 가격(높은 매수가 70000)부터 50씩 체결, 체결 2건
func TestMatch_SellSweepsBidLevels(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e,
		limit(1, domain.TRADING_BUY, 70000, 50), // 더 높은 매수가 → 먼저
		limit(2, domain.TRADING_BUY, 69900, 50),
	)

	// 매도 100 @ 69900 → 70000 50 + 69900 50
	if err := e.match(limit(3, domain.TRADING_SELL, 69900, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	trades := readTrades(t, e)
	if len(trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(trades))
	}
	// 더 좋은 가격(높은 매수가)부터 체결
	if trades[0].Price != 70000 || trades[0].Quantity != 50 {
		t.Fatalf("trade[0] = %+v, want price=70000 qty=50", trades[0])
	}
	if trades[1].Price != 69900 || trades[1].Quantity != 50 {
		t.Fatalf("trade[1] = %+v, want price=69900 qty=50", trades[1])
	}
	// 양쪽 매수 전량 소진, 매도 잔량 없음
	if _, resting := remaining(t, e, 3); resting {
		t.Fatalf("sell taker should be fully filled")
	}
}

// [시나리오] 매수 100@101 이 @100·@101 만 체결하고 @102 는 안 건드림 (A9)
// [기대]    60 체결·@102 무손상, 잔량 40 이 101 에 등록(최우선 매수호가=101)
func TestMatch_MarketableLimit_PartialSweepThenRest(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e,
		limit(1, domain.TRADING_SELL, 100, 30),
		limit(2, domain.TRADING_SELL, 101, 30),
		limit(3, domain.TRADING_SELL, 102, 30),
	)

	// 매수 100 @ 101 → 30@100 + 30@101 = 60 체결, 40 은 101 에 등록
	if err := e.match(limit(4, domain.TRADING_BUY, 101, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	trades := readTrades(t, e)
	if len(trades) != 2 {
		t.Fatalf("trades = %d, want 2", len(trades))
	}
	// @102 대기 매도는 손대지 않음
	if rem, resting := remaining(t, e, 3); !resting || rem != 30 {
		t.Fatalf("ask@102 should be untouched, got (%d, %v)", rem, resting)
	}
	// 매수 테이커 잔량 40 이 101 에 등록
	rem, resting := remaining(t, e, 4)
	if !resting || rem != 40 {
		t.Fatalf("buy remainder = (%d, %v), want (40, true)", rem, resting)
	}
	if p, ok := e.state.OrderBooks.Get(testStockID).BestBid(); !ok || p != 101 {
		t.Fatalf("best bid = (%d, %v), want (101, true)", p, ok)
	}
}

// [시나리오] 시장가 매수(60)가 두 매도 호가(70000,70100)를 전량 스윕 (B2)
// [기대]    체결 2건(70000→70100), 잔량 없음·호가창 매도측 비움
func TestMatch_MarketOrder_FullSweep(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e,
		limit(1, domain.TRADING_SELL, 70000, 30),
		limit(2, domain.TRADING_SELL, 70100, 30),
	)

	// 시장가 매수 60 → 두 호가 모두 쓸어담아 전량 체결
	if err := e.match(market(3, domain.TRADING_BUY, 100_000, 60)); err != nil {
		t.Fatalf("match: %v", err)
	}

	trades := readTrades(t, e)
	if len(trades) != 2 || trades[0].Price != 70000 || trades[1].Price != 70100 {
		t.Fatalf("trades = %+v, want 70000 then 70100", trades)
	}
	// 시장가는 잔량이 없어야 하고 등록되지 않음
	if _, resting := remaining(t, e, 3); resting {
		t.Fatalf("market order must not rest")
	}
	ob := e.state.OrderBooks.Get(testStockID)
	if _, ok := ob.BestAsk(); ok {
		t.Fatalf("book should have no asks left")
	}
}

// [시나리오] 빈 호가창에 시장가 매도(50) — 유동성 없음 (B3)
// [기대]    체결 0건, 호가창에 등록되지 않음
func TestMatch_MarketOrder_NoLiquidity(t *testing.T) {
	e := newMatchEngine(t)

	// 빈 매수 호가창에 시장가 매도
	if err := e.match(market(1, domain.TRADING_SELL, 40_000, 50)); err != nil {
		t.Fatalf("match: %v", err)
	}
	if trades := readTrades(t, e); len(trades) != 0 {
		t.Fatalf("trades = %d, want 0", len(trades))
	}
	if _, resting := remaining(t, e, 1); resting {
		t.Fatalf("market order must not rest")
	}
}

// [시나리오] 100 보유/가용 계좌가 40 매도(테이커)로 전량 체결 (C4)
// [기대]    잔량 60·가용수량 60 (진입 예약+정산 이중 차감 없음)
func TestSettle_SellTaker_AvailableQuantityNotDoubleDecremented(t *testing.T) {
	const buyer, seller int32 = 100, 200
	e := newMatchEngine(t)
	putAccount(e, buyer, 10_000_000)
	putAccount(e, seller, 0)
	putStock(e, testStockID, 60_000, domain.LISTED)
	putHolding(e, seller, testStockID, 100) // Quantity=100, AvailableQuantity=100

	seed(t, e, ord(1, buyer, domain.TRADING_BUY, 70_000, 40))
	if err := e.match(ord(2, seller, domain.TRADING_SELL, 70_000, 40)); err != nil {
		t.Fatalf("match: %v", err)
	}

	sh := holdingOf(t, e, seller)
	if sh.Quantity != 60 {
		t.Fatalf("seller quantity = %d, want 60", sh.Quantity)
	}
	if sh.AvailableQuantity != 60 {
		t.Fatalf("seller availableQuantity = %d, want 60 (이중 차감/언더플로우)", sh.AvailableQuantity)
	}
}

// [시나리오] 매도 100 등록(가용 100→0) 후 매수 테이커가 전량 체결 (C4 메이커 경로)
// [기대]    잔량 0·가용수량 0 (정산 시 추가 차감으로 언더플로우 나지 않음)
func TestSettle_RestingSeller_FilledByBuyTaker_AvailableConserved(t *testing.T) {
	const buyer, seller int32 = 100, 200
	e := newMatchEngine(t)
	putAccount(e, buyer, 10_000_000)
	putAccount(e, seller, 0)
	putStock(e, testStockID, 60_000, domain.LISTED)
	putHolding(e, seller, testStockID, 100)

	// 매도 100 @ 70000 을 match 경로로 등록(빈 호가창 → 예약 후 등록)
	if err := e.match(ord(1, seller, domain.TRADING_SELL, 70_000, 100)); err != nil {
		t.Fatalf("seed sell via match: %v", err)
	}
	// 매수 테이커가 전량 체결
	if err := e.match(ord(2, buyer, domain.TRADING_BUY, 70_000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	sh := holdingOf(t, e, seller)
	if sh.Quantity != 0 {
		t.Fatalf("seller quantity = %d, want 0", sh.Quantity)
	}
	if sh.AvailableQuantity != 0 {
		t.Fatalf("seller availableQuantity = %d, want 0 (언더플로우 의심)", sh.AvailableQuantity)
	}
}

// [시나리오] 지정가 70000 으로 예약했으나 60000 에 전량 체결 (C5 가격개선)
// [기대]    차액 (70000-60000)*100 환원 → Balance·AvailableBalance 모두 4,000,000
func TestSettle_BuyPriceImprovement_ReleasesExcessReserve(t *testing.T) {
	const buyer, seller int32 = 100, 200
	e := newMatchEngine(t)
	putAccount(e, buyer, 10_000_000)
	putAccount(e, seller, 0)
	putStock(e, testStockID, 60_000, domain.LISTED)
	putHolding(e, seller, testStockID, 100)

	// 대기 매도 100 @ 60000, 매수 테이커 100 @ 70000 → 60000 에 체결(가격개선)
	seed(t, e, ord(1, seller, domain.TRADING_SELL, 60_000, 100))
	if err := e.match(ord(2, buyer, domain.TRADING_BUY, 70_000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	b := acctOf(t, e, buyer)
	if b.Balance != 4_000_000 {
		t.Fatalf("buyer balance = %d, want 4000000", b.Balance)
	}
	if b.AvailableBalance != 4_000_000 {
		t.Fatalf("buyer availableBalance = %d, want 4000000 (가격개선분 1,000,000 미환원)", b.AvailableBalance)
	}
}

// [시나리오] 매수 100@70000 이 30@60000 만 체결, 70 은 잔량 등록 (C5 부분체결)
// [기대]    환원은 체결분(30)만 → 가용 3,300,000 (잔량 70 잠금 유지, FilledQuantity 기준)
func TestSettle_BuyPriceImprovement_PartialFill_KeepsRestingReserve(t *testing.T) {
	const buyer, seller int32 = 100, 200
	e := newMatchEngine(t)
	putAccount(e, buyer, 10_000_000)
	putAccount(e, seller, 0)
	putStock(e, testStockID, 60_000, domain.LISTED)
	putHolding(e, seller, testStockID, 30)

	// 매수 100 @ 70000, 대기 매도 30 @ 60000 → 30 체결(가격개선), 70 잔량 등록
	seed(t, e, ord(1, seller, domain.TRADING_SELL, 60_000, 30))
	if err := e.match(ord(2, buyer, domain.TRADING_BUY, 70_000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	// 잔량 70 이 70000 에 등록됐는지
	if rem, resting := remaining(t, e, 2); !resting || rem != 70 {
		t.Fatalf("buy remainder = (%d, %v), want (70, true)", rem, resting)
	}

	b := acctOf(t, e, buyer)
	// 지출 1,800,000 + 잔량 잠금 70*70000=4,900,000 → 가용 = 10,000,000 - 6,700,000
	if b.Balance != 8_200_000 {
		t.Fatalf("buyer balance = %d, want 8200000", b.Balance)
	}
	if b.AvailableBalance != 3_300_000 {
		t.Fatalf("buyer availableBalance = %d, want 3300000 (잔량 잠금 유지)", b.AvailableBalance)
	}
}
