package ledger

import (
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// [시나리오] 종목 추가 후 조회, 없는 종목 조회
// [기대]    있는 종목은 내용과 함께 반환, 없는 종목은 ok=false
func TestStocks_Get(t *testing.T) {
	s := NewStocks()
	s.Upsert(&domain.Stock{Id: 10, Price: 70_000, Status: domain.LISTED})

	st, ok := s.Get(10)
	if !ok || st.Price != 70_000 || st.Status != domain.LISTED {
		t.Errorf("Get(10) = (%+v, %v), want price=70000 status=LISTED", st, ok)
	}

	if _, ok := s.Get(999); ok {
		t.Error("없는 종목 Get 은 ok=false 여야 함")
	}
}

// [시나리오] 같은 Id 로 Upsert 를 두 번 호출
// [기대]    나중 값으로 덮어써짐
func TestStocks_Upsert_Overwrite(t *testing.T) {
	s := NewStocks()
	s.Upsert(&domain.Stock{Id: 10, Price: 70_000, Status: domain.LISTED})
	// 같은 Id 로 갱신
	s.Upsert(&domain.Stock{Id: 10, Price: 65_000, Status: domain.SUSPENDED})

	st, _ := s.Get(10)
	if st.Price != 65_000 || st.Status != domain.SUSPENDED {
		t.Errorf("Upsert 갱신 후 = %+v, want price=65000 status=SUSPENDED", st)
	}
}

// [시나리오] Get 으로 받은 종목의 필드를 수정
// [기대]    원장의 원본은 변하지 않음 (복사본 반환)
func TestStocks_Get_ReturnsCopy(t *testing.T) {
	s := NewStocks()
	s.Upsert(&domain.Stock{Id: 10, Price: 70_000, Status: domain.LISTED})

	got, _ := s.Get(10)
	got.Price = 1 // 복사본 수정

	again, _ := s.Get(10)
	if again.Price != 70_000 {
		t.Errorf("Get 복사본 수정이 원장에 반영됨: price = %d, want 70000", again.Price)
	}
}
