package core

import (
	"math"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// ── 원장 시드 헬퍼 ──────────────────────────────────────────────

func putAccount(e *Engine, id int32, available uint64) {
	e.state.Accounts.Upsert(&domain.Account{Id: id, Balance: available, AvailableBalance: available})
}

func putStock(e *Engine, id int32, price uint64, status domain.StockStatus) {
	e.state.Stocks.Upsert(&domain.Stock{Id: id, Price: price, Status: status})
}

func putHolding(e *Engine, accountID, stockID int32, available uint64) {
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId:         accountID,
		StockId:           stockID,
		Quantity:          available,
		AvailableQuantity: available,
	})
}

const (
	acc   int32 = 10
	stkID int32 = 1
)

func buy(price, qty uint64, ot domain.OrderType) domain.Order {
	return domain.Order{Id: 1, AccountId: acc, StockId: stkID, Price: price, Quantity: qty, OrderType: ot, TradingType: domain.TRADING_BUY}
}

func sell(price, qty uint64, ot domain.OrderType) domain.Order {
	return domain.Order{Id: 1, AccountId: acc, StockId: stkID, Price: price, Quantity: qty, OrderType: ot, TradingType: domain.TRADING_SELL}
}

// 검증 통과/거부를 한 줄로 단언
func wantOK(t *testing.T, e *Engine, order domain.Order) {
	t.Helper()
	if err := e.validateOrder(order); err != nil {
		t.Fatalf("validateOrder = %v, want nil", err)
	}
}

func wantReject(t *testing.T, e *Engine, order domain.Order, why string) {
	t.Helper()
	if err := e.validateOrder(order); err == nil {
		t.Fatalf("validateOrder = nil, want reject (%s)", why)
	}
}

// ── 존재 여부 ──────────────────────────────────────────────────

// [시나리오] 종목은 있으나 계좌가 원장에 없음
// [기대]    매수 주문 거부
func TestValidate_AccountMissing(t *testing.T) {
	e := newMatchEngine(t)
	putStock(e, stkID, 1000, domain.LISTED) // 계좌 없음
	wantReject(t, e, buy(1000, 1, domain.ORDER_LIMIT), "account missing")
}

// [시나리오] 계좌는 있으나 종목이 원장에 없음
// [기대]    매수 주문 거부
func TestValidate_StockMissing(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 1_000_000) // 종목 없음
	wantReject(t, e, buy(1000, 1, domain.ORDER_LIMIT), "stock missing")
}

// ── 거래 가능 상태(상장) ────────────────────────────────────────

// [시나리오] 거래정지(SUSPENDED) 종목에 매수
// [기대]    거래 불가 상태이므로 거부
func TestValidate_StockSuspended_BuyRejected(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 1_000_000)
	putStock(e, stkID, 1000, domain.SUSPENDED)
	wantReject(t, e, buy(1000, 1, domain.ORDER_LIMIT), "suspended stock")
}

// [시나리오] 상장폐지(DELISTED) 종목에 매도(보유 있음)
// [기대]    거래 불가 상태이므로 거부
func TestValidate_StockDelisted_SellRejected(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 1_000_000)
	putStock(e, stkID, 1000, domain.DELISTED)
	putHolding(e, acc, stkID, 100)
	wantReject(t, e, sell(1000, 1, domain.ORDER_LIMIT), "delisted stock")
}

// [시나리오] 상장(LISTED) 종목에 정상 매수
// [기대]    검증 통과
func TestValidate_StockListed_OK(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 1_000_000)
	putStock(e, stkID, 1000, domain.LISTED)
	wantOK(t, e, buy(1000, 5, domain.ORDER_LIMIT))
}

// ── 기본값(수량/가격) ──────────────────────────────────────────

// [시나리오] 수량이 0 인 주문
// [기대]    거부
func TestValidate_ZeroQuantity(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 1_000_000)
	putStock(e, stkID, 1000, domain.LISTED)
	wantReject(t, e, buy(1000, 0, domain.ORDER_LIMIT), "zero quantity")
}

// [시나리오] 가격이 0 인 지정가 주문
// [기대]    거부
func TestValidate_LimitZeroPrice(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 1_000_000)
	putStock(e, stkID, 1000, domain.LISTED)
	wantReject(t, e, buy(0, 10, domain.ORDER_LIMIT), "limit with zero price")
}

// ── 매수 가능 금액 ─────────────────────────────────────────────

// [시나리오] 가용잔고가 매수 비용과 정확히 일치(350,000 = 70000*5)
// [기대]    경계값 통과
func TestValidate_Buy_ExactBalance_OK(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 350_000) // 70000 * 5
	putStock(e, stkID, 1000, domain.LISTED)
	wantOK(t, e, buy(70_000, 5, domain.ORDER_LIMIT)) // 경계: 정확히 일치
}

// [시나리오] 가용잔고가 매수 비용보다 1원 부족
// [기대]    거부
func TestValidate_Buy_OneShort_Rejected(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 349_999) // 1원 부족
	putStock(e, stkID, 1000, domain.LISTED)
	wantReject(t, e, buy(70_000, 5, domain.ORDER_LIMIT), "1 short of cost")
}

// [시나리오] 시장가 주문(API가 상/하한가를 price 로 채워 보냄) vs 가격 0
// [기대]    가격이 있으면 통과, 가격 0 이면 거부
func TestValidate_MarketOrder_PriceRequired(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 1_000_000)
	putStock(e, stkID, 1_000, domain.LISTED)
	putHolding(e, acc, stkID, 100)

	// 상/하한가가 채워진 시장가 → 통과
	wantOK(t, e, buy(1_000, 10, domain.ORDER_MARKET))
	wantOK(t, e, sell(1_000, 10, domain.ORDER_MARKET))

	// 가격이 0 인 시장가 → 거부
	wantReject(t, e, buy(0, 10, domain.ORDER_MARKET), "market buy with zero price")
	wantReject(t, e, sell(0, 10, domain.ORDER_MARKET), "market sell with zero price")
}

// [시나리오] price*qty 가 uint64 범위를 초과(오버플로우)
// [기대]    오버플로우로 잔고 검사를 우회하지 못하고 거부
func TestValidate_Buy_CostOverflow_Rejected(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, math.MaxUint64)
	putStock(e, stkID, 1000, domain.LISTED)
	// price * qty 가 uint64 범위 초과 → 오버플로우로 잔고 검사 우회 방지
	wantReject(t, e, buy(math.MaxUint64, 2, domain.ORDER_LIMIT), "cost overflow")
}

// ── 매도 가능 수량 ─────────────────────────────────────────────

// [시나리오] 보유 종목이 없는 계좌가 매도
// [기대]    거부
func TestValidate_Sell_NoHolding_Rejected(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 0)
	putStock(e, stkID, 1000, domain.LISTED) // 보유 종목 없음
	wantReject(t, e, sell(1000, 1, domain.ORDER_LIMIT), "no holding")
}

// [시나리오] 보유 전량(100)을 매도
// [기대]    경계값 통과
func TestValidate_Sell_ExactQuantity_OK(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 0)
	putStock(e, stkID, 1000, domain.LISTED)
	putHolding(e, acc, stkID, 100)
	wantOK(t, e, sell(1000, 100, domain.ORDER_LIMIT)) // 경계: 보유 전량
}

// [시나리오] 보유(100)보다 많은 101 매도
// [기대]    거부
func TestValidate_Sell_MoreThanAvailable_Rejected(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 0)
	putStock(e, stkID, 1000, domain.LISTED)
	putHolding(e, acc, stkID, 100)
	wantReject(t, e, sell(1000, 101, domain.ORDER_LIMIT), "sell more than held")
}

// [시나리오] 총 100주 보유하나 가용은 30(70은 기존 주문에 묶임), 50 매도 시도
// [기대]    가용 초과(50)는 거부, 가용분(30)까지는 통과
func TestValidate_Sell_AvailableLessThanTotal_Rejected(t *testing.T) {
	e := newMatchEngine(t)
	putAccount(e, acc, 0)
	putStock(e, stkID, 1000, domain.LISTED)
	// 총 100주 보유, 그러나 30주만 가용 (70주는 기존 매도 주문에 묶임)
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId: acc, StockId: stkID, Quantity: 100, AvailableQuantity: 30,
	})
	wantReject(t, e, sell(1000, 50, domain.ORDER_LIMIT), "available < requested")
	wantOK(t, e, sell(1000, 30, domain.ORDER_LIMIT)) // 가용분까지는 OK
}

// ── EDIT/CANCEL 은 계좌/종목/잔고 검사 대상 아님 ────────────────

// [시나리오] 원장이 비어도 CANCEL 은 TargetId 만 검사
// [기대]    TargetId 있으면 통과, 0 이면 거부
func TestValidate_CancelChecksTargetOnly(t *testing.T) {
	e := newMatchEngine(t) // 원장 비어있음
	wantOK(t, e, domain.Order{Id: 1, TargetId: 5, TradingType: domain.TRADING_CANCEL})
	wantReject(t, e, domain.Order{Id: 1, TargetId: 0, TradingType: domain.TRADING_CANCEL}, "invalid target")
}
