package ledger

import (
	"bytes"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

//////////////////////////////////////////////
// ------------- Setting Func ------------- //
//////////////////////////////////////////////

// 모든 상태를 채우는 함수
func buildSampleState() *State {
	s := NewState()

	// 계좌
	s.Accounts.Upsert(&domain.Account{Id: 1, Balance: 1_000_000, AvailableBalance: 800_000})
	s.Accounts.Upsert(&domain.Account{Id: 2, Balance: 500_000, AvailableBalance: 500_000})

	// 주식
	s.Stocks.Upsert(&domain.Stock{Id: 10, Price: 70_000, Status: domain.LISTED})
	s.Stocks.Upsert(&domain.Stock{Id: 20, Price: 0, Status: domain.SUSPENDED})

	// 주식 보유
	s.StockBalances.Upsert(&domain.StockBalance{
		AccountId: 1, StockId: 10,
		Quantity: 100, AvailableQuantity: 80,
		Average: 65_000, TotalBuyAmount: 6_500_000,
	})
	s.StockBalances.Upsert(&domain.StockBalance{
		AccountId: 2, StockId: 20,
		Quantity: 5, AvailableQuantity: 5,
		Average: 1_000, TotalBuyAmount: 5_000,
	})

	// 호가창 — 종목 10: 매수 100원에 id1,id2(FIFO) / 매수 99원 id3 / 매도 105원 id4
	ob10 := s.OrderBooks.Get(10)
	ob10.Add(domain.Order{Id: 1, AccountId: 1, StockId: 10, Price: 100, Quantity: 10, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY})
	ob10.Add(domain.Order{Id: 2, AccountId: 2, StockId: 10, Price: 100, Quantity: 5, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY})
	ob10.Add(domain.Order{Id: 3, AccountId: 1, StockId: 10, Price: 99, Quantity: 7, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY})
	ob10.Add(domain.Order{Id: 4, AccountId: 2, StockId: 10, Price: 105, Quantity: 3, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_SELL})

	// 호가창 — 종목 20: 매도 1000원 id5 (지정가)
	// NOTE: 시장가(ORDER_MARKET)는 호가창에 남을 수 없으므로 테스트 데이터에서 제외
	ob20 := s.OrderBooks.Get(20)
	ob20.Add(domain.Order{Id: 5, AccountId: 1, StockId: 20, Price: 1_000, Quantity: 2, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_SELL})

	return s
}

// State를 proto 스냅샷으로 변환
func snapshotOf(s *State) *ledgerpb.LedgerSnapshot {
	return &ledgerpb.LedgerSnapshot{
		Version:       snapshotVersion,
		Accounts:      s.Accounts.toProto(),
		StockBalances: s.StockBalances.toProto(),
		Stocks:        s.Stocks.toProto(),
		OrderBooks:    s.OrderBooks.toProto(),
	}
}

// 호가창 평탄화 결과의 주문 ID 시퀀스 추출 (FIFO 순서 검증용)
func orderIDs(ob *OrderBook) []int64 {
	orders := ob.orders()
	ids := make([]int64, len(orders))
	for i, o := range orders {
		ids[i] = o.Id
	}
	return ids
}

//////////////////////////////////////////////
// ------------- Testing Func ------------- //
//////////////////////////////////////////////

// 전체 상태를 직렬화 → 역직렬화했을 때 모든 데이터가 동일하게 복원되는지 검증
func TestState_SerializeRestore_RoundTrip(t *testing.T) {
	original := buildSampleState()

	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Serialize returned empty data")
	}

	restored := NewState()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// 1) 직렬화 한 후 복원본 값이 원본 값과 동일해야 한다.
	if !proto.Equal(snapshotOf(original), snapshotOf(restored)) {
		t.Errorf("restored state differs from original\noriginal: %v\nrestored: %v", snapshotOf(original), snapshotOf(restored))
	}

	// 2) 복원본을 다시 직렬화하면 기존 직렬화 값과 동일해야한다.
	data2, err := restored.Serialize()
	if err != nil {
		t.Fatalf("re-Serialize: %v", err)
	}
	if !bytes.Equal(data, data2) {
		t.Errorf("re-serialized bytes differ from original (len %d vs %d)", len(data), len(data2))
	}
}

// 직렬화/역직렬화 후에도 호가창 FIFO 순서가 보존되는지 명시적으로 검증
func TestState_SerializeRestore_PreservesOrderBookFIFO(t *testing.T) {
	original := buildSampleState()

	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := NewState()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// 기존의 호가창 순서와 복원본 호가창 순서가 동일해야한다. (값)
	want := []int64{3, 1, 2, 4}
	got := orderIDs(restored.OrderBooks.Get(10))
	if !slices.Equal(got, want) {
		t.Errorf("stock 10 order sequence = %v, want %v", got, want)
	}

	// 원본과 복원본의 순서가 동일해야 한다.
	if origIDs := orderIDs(original.OrderBooks.Get(10)); !slices.Equal(got, origIDs) {
		t.Errorf("restored sequence %v differs from original %v", got, origIDs)
	}
}

// 복원된 개별 필드 값이 정확한지 공개 게터로 점검
func TestState_SerializeRestore_FieldValues(t *testing.T) {
	original := buildSampleState()
	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	restored := NewState()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// 계좌
	if acc, ok := restored.Accounts.Get(1); !ok {
		t.Error("account 1 was not restored")
	} else if acc.Balance != 1_000_000 || acc.AvailableBalance != 800_000 {
		t.Errorf("account 1 = %+v, balance/availableBalance mismatch", acc)
	}

	// 주식
	if st, ok := restored.Stocks.Get(20); !ok {
		t.Error("stock 20 was not restored")
	} else if st.Status != domain.SUSPENDED {
		t.Errorf("stock 20 status = %d, want SUSPENDED(%d)", st.Status, domain.SUSPENDED)
	}

	// 주식 보유
	if sb, ok := restored.StockBalances.Get(1, 10); !ok {
		t.Error("stock balance (1,10) was not restored")
	} else if sb.Quantity != 100 || sb.Average != 65_000 {
		t.Errorf("stock balance (1,10) = %+v, quantity/average mismatch", sb)
	}
}

// 데이터가 전혀 없는 빈 상태도 안전하게 왕복되는지 검증
func TestState_SerializeRestore_Empty(t *testing.T) {
	original := NewState()

	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}

	restored := NewState()
	if err := restored.Restore(data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if !proto.Equal(snapshotOf(original), snapshotOf(restored)) {
		t.Error("empty-state round-trip differs from original")
	}
}

// 스냅샷 버전이 맞지 않으면 Restore 가 에러를 반환해야 한다.
func TestState_Restore_VersionMismatch(t *testing.T) {
	data, err := proto.Marshal(&ledgerpb.LedgerSnapshot{Version: snapshotVersion + 1})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if err := NewState().Restore(data); err == nil {
		t.Error("Restore did not return an error on version mismatch")
	}
}

// 깨진 바이트를 넣으면 Restore 가 에러를 반환해야 한다.
func TestState_Restore_CorruptData(t *testing.T) {
	if err := NewState().Restore([]byte{0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Error("Restore did not return an error on corrupt data")
	}
}
