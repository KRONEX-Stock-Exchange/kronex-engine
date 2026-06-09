package core

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func (e *Engine) handleOrder(d Delivery, data json.RawMessage) error {
	var order domain.Order
	if err := json.Unmarshal(data, &order); err != nil {
		log.Printf("engine: decode order: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received order %+v", order)

	// 유효성 검사
	if err := validateOrder(order); err != nil {
		log.Printf("engine: invalid order %d: %v", order.Id, err)
		return d.Nack(false)
	}

	if err := e.route(order); err != nil {
		log.Printf("engine: route order %d: %v", order.Id, err)
		return d.Nack(false)
	}

	return d.Ack()
}

// 주문 유효성 검사
// TODO: 원장 연결 후 실제 원장에 존재 하는지 추가해야됨
func validateOrder(order domain.Order) error {
	if order.Id <= 0 {
		return fmt.Errorf("invalid order id %d", order.Id)
	}

	switch order.TradingType {
	case domain.TRADING_BUY, domain.TRADING_SELL:
		if order.AccountId <= 0 {
			return fmt.Errorf("invalid account id %d", order.AccountId)
		}
		if order.StockId <= 0 {
			return fmt.Errorf("invalid stock id %d", order.StockId)
		}
		if order.Quantity == 0 {
			return fmt.Errorf("quantity must be greater than 0")
		}
	case domain.TRADING_EDIT, domain.TRADING_CANCEL:
		if order.TargetId <= 0 {
			return fmt.Errorf("invalid target id %d", order.TargetId)
		}
	default:
		return fmt.Errorf("unknown trading type %d", order.TradingType)
	}
	return nil
}

func (e *Engine) route(order domain.Order) error {
	switch order.TradingType {
	case domain.TRADING_BUY, domain.TRADING_SELL:
		return e.match(order)
	case domain.TRADING_EDIT:
		return e.edit(order)
	case domain.TRADING_CANCEL:
		return e.cancel(order)
	default:
		return fmt.Errorf("unknown trading type %d", order.TradingType)
	}
}

// TODO: 호가창 체결 로직 연결
func (e *Engine) match(order domain.Order) error {
	log.Printf("engine: match order id=%d stock=%d price=%d qty=%d", order.Id, order.StockId, order.Price, order.Quantity)
	return nil
}

// TODO: 주문 정정 로직 연결
func (e *Engine) edit(order domain.Order) error {
	log.Printf("engine: edit order id=%d", order.Id)
	return nil
}

// TODO: 주문 취소 로직 연결
func (e *Engine) cancel(order domain.Order) error {
	log.Printf("engine: cancel order id=%d", order.Id)
	return nil
}
