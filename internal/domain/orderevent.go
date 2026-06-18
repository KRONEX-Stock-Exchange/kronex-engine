package domain

// Output WAL에 들어갈 주문 처리 이벤트 형식
type OrderEvent struct {
	OrderId        int64  `json:"orderId,string"`
	AccountId      int32  `json:"accountId,string"`
	StockId        int32  `json:"stockId,string"`
	Price          uint64 `json:"price,string"`
	Quantity       uint64 `json:"quantity,string"`
	FilledQuantity uint64 `json:"filledQuantity,string"`
}

// 유효성 검사 실패로 거부된 주문 이벤트
type OrderRejected struct {
	OrderId int64  `json:"orderId,string"`
	Reason  string `json:"reason"`
}
