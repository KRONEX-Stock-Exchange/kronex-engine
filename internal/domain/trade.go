package domain

type Trade struct {
	Id           int64  `json:"id,string"`
	StockId      int32  `json:"stockId,string"`
	Price        uint64 `json:"price,string"`
	Quantity     uint64 `json:"quantity,string"`
	MakerOrderId int64  `json:"makerOrderId,string"` // 기존 주문
	TakerOrderId int64  `json:"takerOrderId,string"` // 신규 주문
}
