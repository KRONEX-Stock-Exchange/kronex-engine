package core

import (
	"errors"
	"fmt"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

type RejectReason string

const (
	RejectInvalidOrder        RejectReason = "INVALID_ORDER"
	RejectInsufficientBalance RejectReason = "INSUFFICIENT_BALANCE"
	RejectInsufficientStock   RejectReason = "INSUFFICIENT_STOCK"
	RejectStockNotTradable    RejectReason = "STOCK_NOT_TRADABLE"
	RejectOrderNotActive      RejectReason = "ORDER_NOT_ACTIVE"
)

type RejectError struct {
	Reason RejectReason
	err    error
}

func (e *RejectError) Error() string { return e.err.Error() }
func (e *RejectError) Unwrap() error { return e.err }

func reject(reason RejectReason, format string, a ...any) *RejectError {
	return &RejectError{Reason: reason, err: fmt.Errorf(format, a...)}
}

func rejectReasonOf(err error) RejectReason {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return RejectInvalidOrder
}

func (e *Engine) appendReject(order domain.Order, err error) error {
	return e.appendOutput(outEvent{PatternOrderRejected, domain.OrderRejected{
		OrderId: order.Id,
		Reason:  string(rejectReasonOf(err)),
	}})
}
