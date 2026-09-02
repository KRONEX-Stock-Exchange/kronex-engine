package core

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func adminRequestCompleted(requestID int64) outEvent {
	return outEvent{PatternAdminRequestCompleted, domain.AdminRequestCompleted{
		Id:          requestID,
		CompletedAt: time.Now().UTC(),
	}}
}

func adminRequestRejected(requestID int64, reason AdminRejectReason) outEvent {
	return outEvent{PatternAdminRequestRejected, domain.AdminRequestRejected{
		Id:          requestID,
		Reason:      string(reason),
		CompletedAt: time.Now().UTC(),
	}}
}

func (e *Engine) handleAdminBalanceAdjust(d Delivery, data json.RawMessage) error {
	var req domain.BalanceAdjust
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("engine: decode balance adjust: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received balance adjust %+v", req)

	if e.dedup.has(PatternAdminBalanceAdjust, req.Id) {
		log.Printf("engine: duplicate balance adjust id=%d, skip", req.Id)
		return d.Ack()
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternAdminBalanceAdjust, req.Id)

	e.applyBalanceAdjust(req)
	return d.Ack()
}

func (e *Engine) handleAdminStockBalanceAdjust(d Delivery, data json.RawMessage) error {
	var req domain.StockBalanceAdjust
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("engine: decode stock balance adjust: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received stock balance adjust %+v", req)

	if e.dedup.has(PatternAdminStockBalanceAdjust, req.Id) {
		log.Printf("engine: duplicate stock balance adjust id=%d, skip", req.Id)
		return d.Ack()
	}

	// Input WAL 작성
	idx, err := e.input.Append(d.Message.Payload)
	if err != nil {
		panic(fmt.Errorf("engine: append input wal: %w", err))
	}
	e.inputSeq = idx
	e.dedup.add(PatternAdminStockBalanceAdjust, req.Id)

	e.applyStockBalanceAdjust(req)
	return d.Ack()
}

func (e *Engine) validateBalanceAdjust(req domain.BalanceAdjust) error {
	if req.Delta == 0 {
		return adminReject(AdminRejectInvalidRequest, "balance adjust delta must be non-zero (id=%d)", req.Id)
	}
	acc, ok := e.state.Accounts.Get(req.AccountId)
	if !ok {
		return adminReject(AdminRejectTargetNotFound, "account not found id=%d", req.AccountId)
	}
	if req.Delta < 0 {
		decrease := uint64(-req.Delta)
		if acc.Balance < decrease || acc.AvailableBalance < decrease {
			return adminReject(AdminRejectInsufficientBalance,
				"balance adjust would underflow account=%d balance=%d availableBalance=%d delta=%d",
				req.AccountId, acc.Balance, acc.AvailableBalance, req.Delta)
		}
	}
	return nil
}

func (e *Engine) applyBalanceAdjust(req domain.BalanceAdjust) {
	if err := e.validateBalanceAdjust(req); err != nil {
		log.Printf("engine: balance adjust %d rejected: %v", req.Id, err)
		if err := e.appendOutput(adminRequestRejected(req.Id, adminRejectReasonOf(err))); err != nil {
			panic(fmt.Errorf("engine: append output wal: %w", err))
		}
		return
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
	if err := e.appendOutput(
		outEvent{PatternAccountUpdated, acc},
		adminRequestCompleted(req.Id),
	); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
	log.Printf("engine: balance adjust done id=%d account=%d balance=%d availableBalance=%d",
		req.Id, acc.Id, acc.Balance, acc.AvailableBalance)
}

func (e *Engine) validateStockBalanceAdjust(req domain.StockBalanceAdjust) error {
	if req.Delta == 0 {
		return adminReject(AdminRejectInvalidRequest, "stock balance adjust delta must be non-zero (id=%d)", req.Id)
	}
	if _, ok := e.state.Accounts.Get(req.AccountId); !ok {
		return adminReject(AdminRejectTargetNotFound, "account not found id=%d", req.AccountId)
	}
	if _, ok := e.state.Stocks.Get(req.StockId); !ok {
		return adminReject(AdminRejectTargetNotFound, "stock not found id=%d", req.StockId)
	}

	holding, ok := e.state.StockBalances.Get(req.AccountId, req.StockId)
	if !ok {
		if req.Delta < 0 {
			return adminReject(AdminRejectInsufficientStock, "stock balance not found account=%d stock=%d", req.AccountId, req.StockId)
		}
		if req.Average == 0 {
			return adminReject(AdminRejectInvalidRequest, "average is required when creating stock balance account=%d stock=%d", req.AccountId, req.StockId)
		}
		return nil
	}

	if req.Delta < 0 {
		decrease := uint64(-req.Delta)
		if holding.Quantity < decrease || holding.AvailableQuantity < decrease {
			return adminReject(AdminRejectInsufficientStock,
				"stock balance adjust would underflow account=%d stock=%d quantity=%d availableQuantity=%d delta=%d",
				req.AccountId, req.StockId, holding.Quantity, holding.AvailableQuantity, req.Delta)
		}
	}
	return nil
}

func (e *Engine) applyStockBalanceAdjust(req domain.StockBalanceAdjust) {
	if err := e.validateStockBalanceAdjust(req); err != nil {
		log.Printf("engine: stock balance adjust %d rejected: %v", req.Id, err)
		if err := e.appendOutput(adminRequestRejected(req.Id, adminRejectReasonOf(err))); err != nil {
			panic(fmt.Errorf("engine: append output wal: %w", err))
		}
		return
	}

	holding, ok := e.state.StockBalances.Get(req.AccountId, req.StockId)
	switch {
	case !ok:
		increase := uint64(req.Delta)
		holding = domain.StockBalance{
			AccountId:         req.AccountId,
			StockId:           req.StockId,
			Quantity:          increase,
			AvailableQuantity: increase,
			Average:           req.Average,
			TotalBuyAmount:    increase * req.Average,
		}
	case req.Delta < 0:
		decrease := uint64(-req.Delta)
		holding.Quantity -= decrease
		holding.AvailableQuantity -= decrease
		holding.TotalBuyAmount -= decrease * holding.Average
		if holding.Quantity == 0 {
			holding.Average = 0
			holding.TotalBuyAmount = 0
		}
	default:
		increase := uint64(req.Delta)
		holding.Quantity += increase
		holding.AvailableQuantity += increase
		holding.TotalBuyAmount += increase * holding.Average
	}

	e.state.StockBalances.Upsert(&holding)
	if err := e.appendOutput(
		outEvent{PatternHoldingUpdated, holding},
		adminRequestCompleted(req.Id),
	); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
	log.Printf("engine: stock balance adjust done id=%d account=%d stock=%d quantity=%d availableQuantity=%d",
		req.Id, holding.AccountId, holding.StockId, holding.Quantity, holding.AvailableQuantity)
}
