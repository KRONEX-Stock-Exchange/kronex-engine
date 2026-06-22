package core

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func (e *Engine) handleAdmin(d Delivery) error {
	var env envelope
	if err := json.Unmarshal(d.Message.Payload, &env); err != nil {
		log.Printf("engine: decode admin envelope: %v", err)
		return d.Nack(false)
	}

	switch env.Pattern {
	// NOTE: 주식 상태 변경으로 통합 할 수도 있었지만, 추후 특정 시간 이후 상장 기능 구현을 위해 분리
	case PatternStockList:
		return e.handleStockListed(d, env.Data)
	case PatternAdminBalanceAdjust:
		return e.handleAdminBalanceAdjust(d, env.Data)
	default:
		log.Printf("engine: unknown admin pattern %q", env.Pattern)
		return d.Nack(false)
	}
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

// TODO: 추후 API Server에서 요청 ID를 발급해 넘긴 후 처리 상태를 업데이트 하는 형태로 수정해야함
func (e *Engine) handleAdminBalanceAdjust(d Delivery, data json.RawMessage) error {
	var req domain.BalanceAdjust
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("engine: decode balance adjust: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received balance adjust %+v", req)

	if req.Delta == 0 {
		log.Printf("engine: invalid balance adjust req=%+v", req)
		return d.Nack(false)
	}

	if e.dedup.has(PatternAdminBalanceAdjust, req.Id) {
		log.Printf("engine: duplicate balance adjust id=%d, skip", req.Id)
		return d.Ack()
	}

	acc, ok := e.state.Accounts.Get(req.AccountId)
	if !ok {
		log.Printf("engine: account not found id=%d", req.AccountId)
		return d.Nack(false)
	}

	if req.Delta < 0 {
		decrease := uint64(-req.Delta)
		if acc.Balance < decrease || acc.AvailableBalance < decrease {
			log.Printf("engine: balance adjust would underflow account=%d balance=%d delta=%d", req.AccountId, acc.Balance, req.Delta)
			return d.Nack(false)
		}
		acc.Balance -= decrease
		acc.AvailableBalance -= decrease
	} else {
		acc.Balance += uint64(req.Delta)
		acc.AvailableBalance += uint64(req.Delta)
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternAdminBalanceAdjust, req.Id)

	e.state.Accounts.Upsert(&acc)
	if err := e.appendOutput(outEvent{PatternAccountUpdated, acc}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}

	log.Printf("engine: balance adjust done account=%d balance=%d availableBalance=%d", acc.Id, acc.Balance, acc.AvailableBalance)
	return d.Ack()
}

func (e *Engine) setStockStatus(stock domain.Stock, status domain.StockStatus, pattern string) {
	stock.Status = status
	e.state.Stocks.Upsert(&stock)

	if err := e.appendOutput(outEvent{pattern, stock}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
}
