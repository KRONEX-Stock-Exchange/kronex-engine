package core

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func (e *Engine) handle(d Delivery) error {
	route, ok := e.routes[d.Queue]
	if !ok {
		log.Printf("engine: no route for queue %q", d.Queue)
		return d.Nack(false)
	}
	return route(d)
}

func (e *Engine) handleData(d Delivery) error {
	var env envelope
	if err := json.Unmarshal(d.Message.Payload, &env); err != nil {
		log.Printf("engine: decode envelope: %v", err)
		return d.Nack(false)
	}

	switch env.Pattern {
	case PatternOrderCreated:
		return e.handleOrder(d, env.Data)
	case PatternAccountCreated:
		return e.handleAccountCreated(d, env.Data)
	default:
		log.Printf("engine: unknown data pattern %q", env.Pattern)
		return d.Nack(false)
	}
}

func (e *Engine) handleOrder(d Delivery, data json.RawMessage) error {
	var order domain.Order
	if err := json.Unmarshal(data, &order); err != nil {
		log.Printf("engine: decode order: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received order %+v", order)

	// 만약 이미 처리한 주문 일경우 Ack 요청으로 버림
	if e.dedup.has(PatternOrderCreated, order.Id) {
		log.Printf("engine: duplicate order id=%d, skip", order.Id)
		return d.Ack()
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternOrderCreated, order.Id)

	// 유효성 검사
	if err := e.validateOrder(order); err != nil {
		log.Printf("engine: invalid order %d: %v", order.Id, err)

		// TODO: 주문 현황을 업데이트 하는 별도 DB Publisher 필요
		return d.Nack(false)
	}

	// 주문 처리
	if err := e.route(order); err != nil {
		log.Printf("engine: route order %d: %v", order.Id, err)

		return err
		// NOTE: 추후 자전거래 방지와 같은 별도 에러가 던져질 경우에는 Nack 처리가 필요함
		// return d.Nack(false)
	}

	return d.Ack()
}

func (e *Engine) handleAccountCreated(d Delivery, data json.RawMessage) error {
	var acc domain.Account
	if err := json.Unmarshal(data, &acc); err != nil {
		log.Printf("engine: decode account: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received account %+v", acc)

	if acc.Id <= 0 {
		log.Printf("engine: invalid account id %d", acc.Id)
		return d.Nack(false)
	}

	// 이미 처리한 등록이면 Ack 로 버림
	if e.dedup.has(PatternAccountCreated, int64(acc.Id)) {
		log.Printf("engine: duplicate account id=%d, skip", acc.Id)
		return d.Ack()
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternAccountCreated, int64(acc.Id))

	e.activateAccount(acc)
	return d.Ack()
}

func (e *Engine) activateAccount(acc domain.Account) {
	e.state.Accounts.Upsert(&acc)

	if err := e.appendOutput(outEvent{PatternAccountActivated, acc}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
}
