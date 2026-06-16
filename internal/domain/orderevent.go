package domain

// Output WAL에 들어갈 주문 처리 이벤트 형식
type OrderEvent struct {
	OrderId        int64
	AccountId      int32
	StockId        int32
	Price          uint64
	Quantity       uint64
	FilledQuantity uint64
}

// 유효성 검사 실패로 거부된 주문 이벤트
type OrderRejected struct {
	OrderId int64
	Reason  string
}
