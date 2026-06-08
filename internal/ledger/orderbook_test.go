package ledger

import (
	"slices"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// 테스트 편의용 주문 생성기
func buyOrder(id int64, price, qty uint64) domain.Order {
	return domain.Order{Id: id, Price: price, Quantity: qty, TradingType: domain.TRADING_BUY}
}

func sellOrder(id int64, price, qty uint64) domain.Order {
	return domain.Order{Id: id, Price: price, Quantity: qty, TradingType: domain.TRADING_SELL}
}

//////////////////////////////////////////////
// ------------ BestBid / BestAsk ----------- //
//////////////////////////////////////////////

// [시나리오] 빈 호가창에서 최우선 호가 조회
// [기대]    BestBid/BestAsk 모두 ok=false
func TestOrderBook_BestBidAsk_Empty(t *testing.T) {
	ob := NewOrderBook()

	if _, ok := ob.BestBid(); ok {
		t.Error("빈 호가창의 BestBid 는 ok=false 여야 함")
	}
	if _, ok := ob.BestAsk(); ok {
		t.Error("빈 호가창의 BestAsk 는 ok=false 여야 함")
	}
}

// [시나리오] 여러 가격대의 매수 주문이 있을 때 BestBid 조회
// [기대]    가장 높은 매수가(102) 반환
func TestOrderBook_BestBid_HighestPrice(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))
	ob.Add(buyOrder(2, 102, 10)) // 최고가
	ob.Add(buyOrder(3, 99, 10))

	price, ok := ob.BestBid()
	if !ok || price != 102 {
		t.Errorf("BestBid = (%d, %v), want (102, true)", price, ok)
	}
}

// [시나리오] 여러 가격대의 매도 주문이 있을 때 BestAsk 조회
// [기대]    가장 낮은 매도가(103) 반환
func TestOrderBook_BestAsk_LowestPrice(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(sellOrder(1, 105, 10))
	ob.Add(sellOrder(2, 103, 10)) // 최저가
	ob.Add(sellOrder(3, 110, 10))

	price, ok := ob.BestAsk()
	if !ok || price != 103 {
		t.Errorf("BestAsk = (%d, %v), want (103, true)", price, ok)
	}
}

//////////////////////////////////////////////
// ----------------- Front ------------------ //
//////////////////////////////////////////////

// [시나리오] 같은 가격에 매수 주문 2개를 순서대로 추가 후 Front 조회
// [기대]    먼저 들어온 주문(id1)이 최우선으로 반환 (FIFO)
func TestOrderBook_Front_FIFO(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10)) // 먼저 들어옴 → 최우선
	ob.Add(buyOrder(2, 100, 5))

	order, ok := ob.Front(domain.TRADING_BUY, 100)
	if !ok {
		t.Fatal("100원 매수 호가의 Front 가 없음")
	}
	if order.Id != 1 {
		t.Errorf("Front Id = %d, want 1 (FIFO 최우선)", order.Id)
	}
}

// [시나리오] 주문 없는 가격대 / 반대편 사이드로 Front 조회
// [기대]    둘 다 ok=false
func TestOrderBook_Front_MissingPriceOrSide(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	// 없는 가격대
	if _, ok := ob.Front(domain.TRADING_BUY, 99); ok {
		t.Error("주문 없는 가격대의 Front 는 ok=false 여야 함")
	}
	// 반대편(매도)엔 주문 없음
	if _, ok := ob.Front(domain.TRADING_SELL, 100); ok {
		t.Error("매도가 없는데 Front(SELL) 가 ok=true")
	}
}

//////////////////////////////////////////////
// ------------------ Get ------------------- //
//////////////////////////////////////////////

// [시나리오] 주문 추가 후 id로 조회, 없는 id로 조회
// [기대]    있는 주문은 내용과 함께 반환, 없는 id는 ok=false
func TestOrderBook_Get(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(7, 100, 10))

	order, ok := ob.Get(7)
	if !ok || order.Id != 7 || order.Quantity != 10 {
		t.Errorf("Get(7) = (%+v, %v), want id=7 qty=10", order, ok)
	}

	if _, ok := ob.Get(999); ok {
		t.Error("없는 주문 Get 은 ok=false 여야 함")
	}
}

// [시나리오] Get 으로 받은 주문의 필드를 수정
// [기대]    원장의 원본은 변하지 않음 (복사본 반환)
func TestOrderBook_Get_ReturnsCopy(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	got, _ := ob.Get(1)
	got.Quantity = 9999 // 복사본 수정

	again, _ := ob.Get(1)
	if again.Quantity != 10 {
		t.Errorf("Get 복사본 수정이 원장에 반영됨: quantity = %d, want 10", again.Quantity)
	}
}

//////////////////////////////////////////////
// ----------------- Cancel ----------------- //
//////////////////////////////////////////////

// [시나리오] 존재하는 주문 취소, 이미 취소된 주문 재취소
// [기대]    첫 취소는 true 후 조회 불가, 재취소는 false
func TestOrderBook_Cancel(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	if !ob.Cancel(1) {
		t.Error("존재하는 주문 Cancel 이 false 반환")
	}
	if _, ok := ob.Get(1); ok {
		t.Error("Cancel 후에도 주문이 조회됨")
	}
	if ob.Cancel(1) {
		t.Error("이미 취소된 주문 Cancel 이 true 반환")
	}
}

// [시나리오] 한 가격대의 마지막 주문을 취소
// [기대]    빈 가격대가 가격 슬라이스(buyPrices)에서도 제거됨
func TestOrderBook_Cancel_CleansPriceSlice(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))
	ob.Add(buyOrder(2, 99, 10))

	ob.Cancel(1) // 100원 가격대 비움

	if slices.Contains(ob.buyPrices, 100) {
		t.Errorf("빈 가격대 100 이 buyPrices 에 남음: %v", ob.buyPrices)
	}
	if !slices.Contains(ob.buyPrices, 99) {
		t.Errorf("99 가격대가 사라짐: %v", ob.buyPrices)
	}
}

//////////////////////////////////////////////
// ------------------ Fill ------------------ //
//////////////////////////////////////////////

// [시나리오] 수량 10 주문을 4만큼 부분 체결
// [기대]    체결량 4 반환, 주문은 남고 FilledQuantity=4
func TestOrderBook_Fill_Partial(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	filled := ob.Fill(1, 4)
	if filled != 4 {
		t.Errorf("Fill(1, 4) = %d, want 4", filled)
	}

	order, ok := ob.Get(1)
	if !ok {
		t.Fatal("부분 체결 후 주문이 사라짐")
	}
	if order.FilledQuantity != 4 {
		t.Errorf("FilledQuantity = %d, want 4", order.FilledQuantity)
	}
}

// [시나리오] 수량 10 주문을 10만큼 전량 체결
// [기대]    체결량 10 반환, 주문과 빈 가격대가 호가창에서 제거
func TestOrderBook_Fill_Full_RemovesOrder(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	filled := ob.Fill(1, 10)
	if filled != 10 {
		t.Errorf("Fill(1, 10) = %d, want 10", filled)
	}
	if _, ok := ob.Get(1); ok {
		t.Error("전량 체결 후에도 주문이 호가창에 남음")
	}
	if slices.Contains(ob.buyPrices, 100) {
		t.Errorf("전량 체결 후 빈 가격대가 buyPrices 에 남음: %v", ob.buyPrices)
	}
}

// [시나리오] 남은 수량(10)보다 큰 999 체결 요청
// [기대]    잔량까지만(10) 체결되고 주문 제거
func TestOrderBook_Fill_ClampsToRemaining(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	// 남은 수량(10)보다 큰 요청 → 10 까지만 채워지고 제거
	filled := ob.Fill(1, 999)
	if filled != 10 {
		t.Errorf("Fill(1, 999) = %d, want 10 (클램프)", filled)
	}
	if _, ok := ob.Get(1); ok {
		t.Error("클램프 전량 체결 후에도 주문이 남음")
	}
}

// [시나리오] 4 부분 체결 후 남은 6에 대해 더 큰 요청
// [기대]    2차에 6만 체결되고 누적 전량 체결로 제거
func TestOrderBook_Fill_PartialThenFull(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	if got := ob.Fill(1, 4); got != 4 {
		t.Fatalf("1차 Fill = %d, want 4", got)
	}
	// 남은 6 에 대해 더 큰 요청 → 6 만 채워지고 제거
	if got := ob.Fill(1, 100); got != 6 {
		t.Errorf("2차 Fill = %d, want 6", got)
	}
	if _, ok := ob.Get(1); ok {
		t.Error("누적 전량 체결 후에도 주문이 남음")
	}
}

// [시나리오] 없는 주문 체결 / want=0 으로 체결
// [기대]    둘 다 체결량 0, 기존 주문은 변경 없음
func TestOrderBook_Fill_NotFoundOrZero(t *testing.T) {
	ob := NewOrderBook()
	ob.Add(buyOrder(1, 100, 10))

	if got := ob.Fill(999, 5); got != 0 {
		t.Errorf("없는 주문 Fill = %d, want 0", got)
	}
	if got := ob.Fill(1, 0); got != 0 {
		t.Errorf("want=0 Fill = %d, want 0", got)
	}
	// 위 호출들이 기존 주문을 건드리지 않았는지
	if order, _ := ob.Get(1); order.FilledQuantity != 0 {
		t.Errorf("무효 Fill 이 주문을 변경함: FilledQuantity = %d", order.FilledQuantity)
	}
}

//////////////////////////////////////////////
// ------------- 가격 슬라이스 정렬 ------------ //
//////////////////////////////////////////////

// [시나리오] 가격을 뒤섞인 순서로 추가
// [기대]    buyPrices 는 오름차순 정렬 유지, BestBid 는 마지막 원소
func TestOrderBook_PriceSlicesSorted(t *testing.T) {
	ob := NewOrderBook()
	// 일부러 뒤섞인 순서로 추가
	for _, p := range []uint64{100, 98, 105, 99, 103} {
		ob.Add(buyOrder(int64(p), p, 1))
	}

	if !slices.IsSorted(ob.buyPrices) {
		t.Errorf("buyPrices 가 정렬돼 있지 않음: %v", ob.buyPrices)
	}
	// 최우선 매수 = 가장 높은 가격 = 슬라이스 마지막
	if price, _ := ob.BestBid(); price != ob.buyPrices[len(ob.buyPrices)-1] {
		t.Errorf("BestBid(%d) 가 buyPrices 마지막(%d)과 불일치", price, ob.buyPrices[len(ob.buyPrices)-1])
	}
}
