package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

type Engine struct {
	con   Consumer
	queue string
}

func NewEngine(con Consumer, queue string) *Engine {
	return &Engine{con: con, queue: queue}
}

func (e *Engine) Run(ctx context.Context) error {
	return e.con.Consume(ctx, e.queue, e.handle)
}

type orderEnvelope struct {
	Pattern string       `json:"pattern"`
	Data    domain.Order `json:"data"`
}

// TODO: WAL 작성 및 중복 처리 추가
func (e *Engine) handle(d Delivery) error {
	// JSON 형식 검사
	var env orderEnvelope
	if err := json.Unmarshal(d.Message.Payload, &env); err != nil {
		log.Printf("engine: decode order: %v", err)
		return d.Nack(false)
	}
	order := env.Data

	log.Printf("engine: received order %+v", order)

	// 주문 라우팅
	if err := e.route(order); err != nil {
		log.Printf("engine: route order %d: %v", order.Id, err)
		return d.Nack(false)
	}

	return d.Ack()
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
