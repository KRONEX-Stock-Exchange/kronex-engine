package core

import (
	"fmt"
	"log"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func (e *Engine) validateTransfer(transfer domain.Transfer) (domain.Account, domain.Account, error) {
	none := domain.Account{}

	// 요청 형식
	if transfer.Id <= 0 {
		return none, none, transferReject(TransferRejectInvalidRequest,
			"invalid transfer id %d", transfer.Id)
	}
	if transfer.Amount == 0 {
		return none, none, transferReject(TransferRejectInvalidRequest,
			"invalid transfer amount %d (id=%d)", transfer.Amount, transfer.Id)
	}
	if transfer.SenderAccountId == transfer.RecipientAccountId {
		return none, none, transferReject(TransferRejectSelfTransfer,
			"self transfer account=%d", transfer.SenderAccountId)
	}

	// 발신 계좌
	if transfer.SenderAccountId <= 0 {
		return none, none, transferReject(TransferRejectInvalidSender,
			"invalid sender account id=%d", transfer.SenderAccountId)
	}
	sender, ok := e.state.Accounts.Get(transfer.SenderAccountId)
	if !ok {
		return none, none, transferReject(TransferRejectInvalidSender,
			"sender account not found id=%d", transfer.SenderAccountId)
	}

	// 수신 계좌
	if transfer.RecipientAccountId <= 0 {
		return none, none, transferReject(TransferRejectInvalidRecipient,
			"invalid recipient account id=%d", transfer.RecipientAccountId)
	}
	recipient, ok := e.state.Accounts.Get(transfer.RecipientAccountId)
	if !ok {
		return none, none, transferReject(TransferRejectInvalidRecipient,
			"recipient account not found id=%d", transfer.RecipientAccountId)
	}

	// 가용 잔액
	if sender.AvailableBalance < transfer.Amount {
		return none, none, transferReject(TransferRejectInsufficientBalance,
			"insufficient balance: need %d, available %d", transfer.Amount, sender.AvailableBalance)
	}

	return sender, recipient, nil
}

func (e *Engine) applyTransfer(transfer domain.Transfer) error {
	completedAt := time.Now().UTC()

	sender, recipient, err := e.validateTransfer(transfer)
	if err != nil {
		log.Printf("engine: transfer %d rejected: %v", transfer.Id, err)
		if err := e.appendTransferReject(transfer, err, completedAt); err != nil {
			return fmt.Errorf("append transfer reject %d: %w", transfer.Id, err)
		}
		return nil
	}

	sender.Balance -= transfer.Amount
	sender.AvailableBalance -= transfer.Amount
	recipient.Balance += transfer.Amount
	recipient.AvailableBalance += transfer.Amount
	e.state.Accounts.Upsert(&sender)
	e.state.Accounts.Upsert(&recipient)

	if err := e.appendOutput(
		outEvent{PatternAccountUpdated, sender},
		outEvent{PatternAccountUpdated, recipient},
		outEvent{PatternTransferCompleted, domain.TransferCompleted{
			Id:                 transfer.Id,
			SenderAccountId:    transfer.SenderAccountId,
			RecipientAccountId: transfer.RecipientAccountId,
			Amount:             transfer.Amount,
			CompletedAt:        completedAt,
		}},
	); err != nil {
		return fmt.Errorf("append transfer output %d: %w", transfer.Id, err)
	}

	log.Printf("engine: transfer done id=%d sender=%d recipient=%d amount=%d",
		transfer.Id, transfer.SenderAccountId, transfer.RecipientAccountId, transfer.Amount)
	return nil
}
