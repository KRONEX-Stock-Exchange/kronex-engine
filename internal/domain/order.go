package domain

type OrderType uint8

const (
	ORDER_LIMIT OrderType = iota
	ORDER_MARKET
)

type TradingType uint8

const (
	TRADING_BUY TradingType = iota
	TRADING_SELL
	TRADING_EDIT
	TRADING_CANCEL
)

type Order struct {
	Id             int64       `json:"id"`
	TargetId       int64       `json:"targetId"`
	AccountId      int32       `json:"accountId"`
	StockId        int32       `json:"stockId"`
	Price          uint64      `json:"price"`
	Quantity       uint64      `json:"quantity"`
	FilledQuantity uint64      `json:"filledQuantity"`
	OrderType      OrderType   `json:"orderType"`
	TradingType    TradingType `json:"tradingType"`
}
