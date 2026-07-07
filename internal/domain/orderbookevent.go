package domain

type OrderBookLevel struct {
	Side     string `json:"side"`
	Price    uint64 `json:"price,string"`
	Quantity uint64 `json:"quantity,string"`
}

type OrderBookUpdated struct {
	StockId int32            `json:"stockId,string"`
	Levels  []OrderBookLevel `json:"levels"`
}
