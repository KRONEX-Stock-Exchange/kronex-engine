package domain

type Trade struct {
	Id           int64
	StockId      int32
	Price        uint64
	Quantity     uint64
	MakerOrderId int64 // 기존 주문
	TakerOrderId int64 // 신규 주문
}
