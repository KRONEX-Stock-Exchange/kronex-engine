package domain

type StockBalance struct {
	AccountId         int32
	StockId           int32
	Quantity          uint64
	AvailableQuantity uint64
	Average           uint64
	TotalBuyAmount    uint64
}
