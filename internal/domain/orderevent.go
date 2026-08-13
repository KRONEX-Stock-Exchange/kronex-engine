package domain

import "time"

// Output WAL에 들어갈 주문 처리 이벤트 형식
type OrderEvent struct {
	Id             int64       `json:"id,string"`
	TargetId       int64       `json:"targetId,string"`
	AccountId      int32       `json:"accountId,string"`
	StockId        int32       `json:"stockId,string"`
	Price          uint64      `json:"price,string"`
	TradingType    TradingType `json:"tradingType"`
	OrderType      OrderType   `json:"orderType"`
	Quantity       uint64      `json:"quantity,string"`
	FilledQuantity uint64      `json:"filledQuantity,string"`
	CreatedAt      *time.Time  `json:"createdAt"` // 옛날 주문은 값이 없을 수 있어 null 허용
	FullyFilledAt  *time.Time  `json:"fullyFilledAt,omitempty"` // 전량 체결 시각 (전량 체결이 아니면 nil)
}

// 유효성 검사 실패로 거부된 주문 이벤트
type OrderRejected struct {
	OrderId int64  `json:"orderId,string"`
	Reason  string `json:"reason"`
}
