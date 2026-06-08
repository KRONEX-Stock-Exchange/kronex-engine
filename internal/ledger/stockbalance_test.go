package ledger

import (
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// [시나리오] 보유 추가 후 조회, 없는 보유 조회
// [기대]    있는 보유는 내용과 함께 반환, 없는 키는 ok=false
func TestStockBalances_Get(t *testing.T) {
	s := NewStockBalances()
	s.Upsert(&domain.StockBalance{
		AccountId: 1, StockId: 10,
		Quantity: 100, AvailableQuantity: 80,
		Average: 65_000, TotalBuyAmount: 6_500_000,
	})

	sb, ok := s.Get(1, 10)
	if !ok || sb.Quantity != 100 || sb.Average != 65_000 {
		t.Errorf("Get(1,10) = (%+v, %v), want quantity=100 average=65000", sb, ok)
	}

	if _, ok := s.Get(999, 10); ok {
		t.Error("없는 보유 Get 은 ok=false 여야 함")
	}
}

// [시나리오] (계좌,종목) 조합이 다른 보유 3개를 추가
// [기대]    각 복합키가 서로 섞이지 않고 정확히 구분됨
func TestStockBalances_CompositeKey(t *testing.T) {
	s := NewStockBalances()
	s.Upsert(&domain.StockBalance{AccountId: 1, StockId: 10, Quantity: 100})
	s.Upsert(&domain.StockBalance{AccountId: 1, StockId: 20, Quantity: 5})  // 같은 계좌, 다른 종목
	s.Upsert(&domain.StockBalance{AccountId: 2, StockId: 10, Quantity: 50}) // 다른 계좌, 같은 종목

	if sb, _ := s.Get(1, 10); sb.Quantity != 100 {
		t.Errorf("(1,10) quantity = %d, want 100", sb.Quantity)
	}
	if sb, _ := s.Get(1, 20); sb.Quantity != 5 {
		t.Errorf("(1,20) quantity = %d, want 5", sb.Quantity)
	}
	if sb, _ := s.Get(2, 10); sb.Quantity != 50 {
		t.Errorf("(2,10) quantity = %d, want 50", sb.Quantity)
	}
}

// [시나리오] 같은 복합키로 Upsert 를 두 번 호출
// [기대]    나중 값으로 덮어써짐
func TestStockBalances_Upsert_Overwrite(t *testing.T) {
	s := NewStockBalances()
	s.Upsert(&domain.StockBalance{AccountId: 1, StockId: 10, Quantity: 100})
	// 같은 복합키로 갱신
	s.Upsert(&domain.StockBalance{AccountId: 1, StockId: 10, Quantity: 120, AvailableQuantity: 90})

	sb, _ := s.Get(1, 10)
	if sb.Quantity != 120 || sb.AvailableQuantity != 90 {
		t.Errorf("Upsert 갱신 후 = %+v, want quantity=120 available=90", sb)
	}
}

// [시나리오] Get 으로 받은 보유의 필드를 수정
// [기대]    원장의 원본은 변하지 않음 (복사본 반환)
func TestStockBalances_Get_ReturnsCopy(t *testing.T) {
	s := NewStockBalances()
	s.Upsert(&domain.StockBalance{AccountId: 1, StockId: 10, Quantity: 100})

	got, _ := s.Get(1, 10)
	got.Quantity = 9999 // 복사본 수정

	again, _ := s.Get(1, 10)
	if again.Quantity != 100 {
		t.Errorf("Get 복사본 수정이 원장에 반영됨: quantity = %d, want 100", again.Quantity)
	}
}
