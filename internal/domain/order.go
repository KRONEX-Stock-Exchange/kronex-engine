package domain

import (
	"encoding/json"
	"fmt"
)

type OrderType uint8

const (
	ORDER_LIMIT OrderType = iota
	ORDER_MARKET
)

// 큐 메세지의 orderType 문자열을 OrderType으로 변환
func (t *OrderType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch s {
	case "LIMIT":
		*t = ORDER_LIMIT
	case "MARKET":
		*t = ORDER_MARKET
	default:
		return fmt.Errorf("unknown order type %q", s)
	}
	return nil
}

type TradingType uint8

const (
	TRADING_BUY TradingType = iota
	TRADING_SELL
	TRADING_EDIT
	TRADING_CANCEL
)

// 큐 메세지의 tradingType 문자열을 TradingType으로 변환
func (t *TradingType) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch s {
	case "BUY":
		*t = TRADING_BUY
	case "SELL":
		*t = TRADING_SELL
	case "EDIT":
		*t = TRADING_EDIT
	case "CANCEL":
		*t = TRADING_CANCEL
	default:
		return fmt.Errorf("unknown trading type %q", s)
	}
	return nil
}

type Order struct {
	Id             int64       `json:"id,string"`
	TargetId       int64       `json:"targetId,string"`
	AccountId      int32       `json:"accountId"`
	StockId        int32       `json:"stockId"`
	Price          uint64      `json:"price,string"`
	Quantity       uint64      `json:"quantity,string"`
	FilledQuantity uint64      `json:"filledQuantity,string"`
	OrderType      OrderType   `json:"orderType"`
	TradingType    TradingType `json:"tradingType"`
}
