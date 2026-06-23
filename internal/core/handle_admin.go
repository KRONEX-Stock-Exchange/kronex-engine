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
	case PatternAdminStockBalanceAdjust:
		return e.handleAdminStockBalanceAdjust(d, env.Data)
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

	if err := e.validateBalanceAdjust(req); err != nil {
		log.Printf("engine: invalid balance adjust: %v", err)
		return d.Nack(false)
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternAdminBalanceAdjust, req.Id)

	acc, err := e.applyBalanceAdjust(req)
	if err != nil {
		log.Printf("engine: balance adjust failed: %v", err)
		return d.Nack(false)
	}

	log.Printf("engine: balance adjust done account=%d balance=%d availableBalance=%d", acc.Id, acc.Balance, acc.AvailableBalance)
	return d.Ack()
}

func (e *Engine) handleAdminStockBalanceAdjust(d Delivery, data json.RawMessage) error {
	var req domain.StockBalanceAdjust
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("engine: decode stock balance adjust: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received stock balance adjust %+v", req)

	if req.Delta == 0 {
		log.Printf("engine: invalid stock balance adjust req=%+v", req)
		return d.Nack(false)
	}

	if e.dedup.has(PatternAdminStockBalanceAdjust, req.Id) {
		log.Printf("engine: duplicate stock balance adjust id=%d, skip", req.Id)
		return d.Ack()
	}

	if err := e.validateStockBalanceAdjust(req); err != nil {
		log.Printf("engine: invalid stock balance adjust: %v", err)
		return d.Nack(false)
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternAdminStockBalanceAdjust, req.Id)

	holding, err := e.applyStockBalanceAdjust(req)
	if err != nil {
		log.Printf("engine: stock balance adjust failed: %v", err)
		return d.Nack(false)
	}

	log.Printf("engine: stock balance adjust done account=%d stock=%d quantity=%d availableQuantity=%d",
		holding.AccountId, holding.StockId, holding.Quantity, holding.AvailableQuantity)
	return d.Ack()
}

func (e *Engine) validateBalanceAdjust(req domain.BalanceAdjust) error {
	acc, ok := e.state.Accounts.Get(req.AccountId)
	if !ok {
		return fmt.Errorf("account not found id=%d", req.AccountId)
	}
	if req.Delta < 0 {
		decrease := uint64(-req.Delta)
		if acc.Balance < decrease || acc.AvailableBalance < decrease {
			return fmt.Errorf("balance adjust would underflow account=%d balance=%d availableBalance=%d delta=%d",
				req.AccountId, acc.Balance, acc.AvailableBalance, req.Delta)
		}
	}
	return nil
}

func (e *Engine) applyBalanceAdjust(req domain.BalanceAdjust) (domain.Account, error) {
	if err := e.validateBalanceAdjust(req); err != nil {
		return domain.Account{}, err
	}

	acc, _ := e.state.Accounts.Get(req.AccountId)
	if req.Delta < 0 {
		decrease := uint64(-req.Delta)
		acc.Balance -= decrease
		acc.AvailableBalance -= decrease
	} else {
		acc.Balance += uint64(req.Delta)
		acc.AvailableBalance += uint64(req.Delta)
	}

	e.state.Accounts.Upsert(&acc)
	if err := e.appendOutput(outEvent{PatternAccountUpdated, acc}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}

	return acc, nil
}

func (e *Engine) validateStockBalanceAdjust(req domain.StockBalanceAdjust) error {
	if _, ok := e.state.Accounts.Get(req.AccountId); !ok {
		return fmt.Errorf("account not found id=%d", req.AccountId)
	}
	if _, ok := e.state.Stocks.Get(req.StockId); !ok {
		return fmt.Errorf("stock not found id=%d", req.StockId)
	}

	holding, ok := e.state.StockBalances.Get(req.AccountId, req.StockId)
	if !ok {
		if req.Delta < 0 {
			return fmt.Errorf("stock balance not found account=%d stock=%d", req.AccountId, req.StockId)
		}
		if req.Average == 0 {
			return fmt.Errorf("average is required when creating stock balance account=%d stock=%d", req.AccountId, req.StockId)
		}
		return nil
	}

	if req.Delta < 0 {
		decrease := uint64(-req.Delta)
		if holding.Quantity < decrease || holding.AvailableQuantity < decrease {
			return fmt.Errorf("stock balance adjust would underflow account=%d stock=%d quantity=%d availableQuantity=%d delta=%d",
				req.AccountId, req.StockId, holding.Quantity, holding.AvailableQuantity, req.Delta)
		}
	}
	return nil
}

func (e *Engine) applyStockBalanceAdjust(req domain.StockBalanceAdjust) (domain.StockBalance, error) {
	if err := e.validateStockBalanceAdjust(req); err != nil {
		return domain.StockBalance{}, err
	}

	holding, ok := e.state.StockBalances.Get(req.AccountId, req.StockId)
	if !ok {
		increase := uint64(req.Delta)
		holding = domain.StockBalance{
			AccountId:         req.AccountId,
			StockId:           req.StockId,
			Quantity:          increase,
			AvailableQuantity: increase,
			Average:           req.Average,
			TotalBuyAmount:    increase * req.Average,
		}
		e.state.StockBalances.Upsert(&holding)
		if err := e.appendOutput(outEvent{PatternHoldingUpdated, holding}); err != nil {
			panic(fmt.Errorf("engine: append output wal: %w", err))
		}
		return holding, nil
	}

	if req.Delta < 0 {
		decrease := uint64(-req.Delta)
		holding.Quantity -= decrease
		holding.AvailableQuantity -= decrease
		holding.TotalBuyAmount -= decrease * holding.Average
		if holding.Quantity == 0 {
			holding.Average = 0
			holding.TotalBuyAmount = 0
		}
	} else {
		increase := uint64(req.Delta)
		holding.Quantity += increase
		holding.AvailableQuantity += increase
		holding.TotalBuyAmount += increase * holding.Average
	}

	e.state.StockBalances.Upsert(&holding)
	if err := e.appendOutput(outEvent{PatternHoldingUpdated, holding}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}

	return holding, nil
}

func (e *Engine) setStockStatus(stock domain.Stock, status domain.StockStatus, pattern string) {
	stock.Status = status
	e.state.Stocks.Upsert(&stock)

	if err := e.appendOutput(outEvent{pattern, stock}); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
}
