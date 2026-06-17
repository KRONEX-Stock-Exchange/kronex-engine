package domain

type StockBalance struct {
	AccountId         int32  `json:"accountId,string"`
	StockId           int32  `json:"stockId,string"`
	Quantity          uint64 `json:"quantity,string"`
	AvailableQuantity uint64 `json:"availableQuantity,string"`
	Average           uint64 `json:"average,string"`
	TotalBuyAmount    uint64 `json:"totalBuyAmount,string"`
}
