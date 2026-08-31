package core

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func (e *Engine) handle(d Delivery) error {
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
	case PatternTransferCreated:
		return e.handleTransferCreated(d, env.Data)
	case PatternStockList:
		return e.handleStockListed(d, env.Data)
	case PatternAdminBalanceAdjust:
		return e.handleAdminBalanceAdjust(d, env.Data)
	case PatternAdminStockBalanceAdjust:
		return e.handleAdminStockBalanceAdjust(d, env.Data)
	default:
		log.Printf("engine: unknown pattern %q", env.Pattern)
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

		// 거부를 Output WAL에 기록
		if err := e.appendReject(order, err); err != nil {
			panic(fmt.Errorf("engine: append output wal: %w", err))
		}
		return d.Ack()
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

func (e *Engine) handleTransferCreated(d Delivery, data json.RawMessage) error {
	var transfer domain.Transfer
	if err := json.Unmarshal(data, &transfer); err != nil {
		log.Printf("engine: decode transfer: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received transfer %+v", transfer)

	// 이미 처리한 송금이면 Ack 로 버림
	if e.dedup.has(PatternTransferCreated, transfer.Id) {
		log.Printf("engine: duplicate transfer id=%d, skip", transfer.Id)
		return d.Ack()
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternTransferCreated, transfer.Id)

	if err := e.applyTransfer(transfer); err != nil {
		log.Printf("engine: transfer %d: %v", transfer.Id, err)
		return err
	}

	return d.Ack()
}

func (e *Engine) handleStockListed(d Delivery, data json.RawMessage) error {
	var stock domain.Stock
	if err := json.Unmarshal(data, &stock); err != nil {
		log.Printf("engine: decode stock: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received stock listing %+v", stock)

	if stock.Id <= 0 {
		log.Printf("engine: invalid stock id %d", stock.Id)
		return d.Nack(false)
	}

	// 이미 처리한 상장이면 Ack 로 버림
	if e.dedup.has(PatternStockList, int64(stock.Id)) {
		log.Printf("engine: duplicate stock id=%d, skip", stock.Id)
		return d.Ack()
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternStockList, int64(stock.Id))

	e.setStockStatus(stock, domain.LISTED, PatternStockListed)
	return d.Ack()
}

func (e *Engine) setStockStatus(stock domain.Stock, status domain.StockStatus, pattern string) {
	stock.Status = status
	e.state.Stocks.Upsert(&stock)

	if err := e.appendOutput(outEvent{pattern, stock}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
}

func (e *Engine) activateAccount(acc domain.Account) {
	e.state.Accounts.Upsert(&acc)

	if err := e.appendOutput(outEvent{PatternAccountActivated, acc}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
}
