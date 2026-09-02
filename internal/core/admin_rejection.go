package core

import (
	"errors"
	"fmt"
)

type AdminRejectReason string

const (
	AdminRejectTargetNotFound      AdminRejectReason = "TARGET_NOT_FOUND"
	AdminRejectInvalidState        AdminRejectReason = "INVALID_STATE"
	AdminRejectInsufficientBalance AdminRejectReason = "INSUFFICIENT_BALANCE"
	AdminRejectInsufficientStock   AdminRejectReason = "INSUFFICIENT_STOCK"
	AdminRejectInvalidRequest      AdminRejectReason = "INVALID_REQUEST"
)

type AdminRejectError struct {
	Reason AdminRejectReason
	err    error
}

func (e *AdminRejectError) Error() string { return e.err.Error() }
func (e *AdminRejectError) Unwrap() error { return e.err }

func adminReject(reason AdminRejectReason, format string, a ...any) *AdminRejectError {
	return &AdminRejectError{Reason: reason, err: fmt.Errorf(format, a...)}
}

func adminRejectReasonOf(err error) AdminRejectReason {
	var re *AdminRejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return AdminRejectInvalidRequest
}
