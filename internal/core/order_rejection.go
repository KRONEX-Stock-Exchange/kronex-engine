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

// err에서 거부 사유 추출 (타입 불명 시 INVALID_ORDER)
func rejectReasonOf(err error) RejectReason {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return RejectInvalidOrder
}

// 거부를 Output WAL에 기록 → publisher가 DB에 REJECTED 반영 및 이벤트 발행
func (e *Engine) appendReject(order domain.Order, err error) error {
	return e.appendOutput(outEvent{PatternOrderRejected, domain.OrderRejected{
		OrderId: order.Id,
		Reason:  string(rejectReasonOf(err)),
	}})
}
