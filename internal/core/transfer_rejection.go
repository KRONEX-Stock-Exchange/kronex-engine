package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

type TransferRejectReason string

const (
	TransferRejectInsufficientBalance TransferRejectReason = "INSUFFICIENT_BALANCE"
	TransferRejectInvalidSender       TransferRejectReason = "INVALID_SENDER"
	TransferRejectInvalidRecipient    TransferRejectReason = "INVALID_RECIPIENT"
	TransferRejectSelfTransfer        TransferRejectReason = "SELF_TRANSFER"
	TransferRejectInvalidRequest      TransferRejectReason = "INVALID_REQUEST"

	// NOTE: 아래 분류를 사용할려면 Account State에 계좌 상태를 추가해야됨
	TransferRejectSenderNotActive    TransferRejectReason = "SENDER_NOT_ACTIVE"
	TransferRejectRecipientNotActive TransferRejectReason = "RECIPIENT_NOT_ACTIVE"
)

type TransferRejectError struct {
	Reason TransferRejectReason
	err    error
}

func (e *TransferRejectError) Error() string { return e.err.Error() }
func (e *TransferRejectError) Unwrap() error { return e.err }

func transferReject(reason TransferRejectReason, format string, a ...any) *TransferRejectError {
	return &TransferRejectError{Reason: reason, err: fmt.Errorf(format, a...)}
}

func transferRejectReasonOf(err error) TransferRejectReason {
	var re *TransferRejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return TransferRejectInvalidRequest
}

func (e *Engine) appendTransferReject(transfer domain.Transfer, err error, completedAt time.Time) error {
	return e.appendOutput(outEvent{PatternTransferRejected, domain.TransferRejected{
		Id:                 transfer.Id,
		SenderAccountId:    transfer.SenderAccountId,
		RecipientAccountId: transfer.RecipientAccountId,
		Amount:             transfer.Amount,
		Reason:             string(transferRejectReasonOf(err)),
		CompletedAt:        completedAt,
	}})
}
