package domain

import "time"

// admin.request.completed
type AdminRequestCompleted struct {
	Id          int64     `json:"id,string"`   // 어드민 요청 ID (admin_requests.id)
	CompletedAt time.Time `json:"completedAt"` // 엔진의 처리 완료 시각
}

// admin.request.rejected
type AdminRequestRejected struct {
	Id          int64     `json:"id,string"`   // 어드민 요청 ID (admin_requests.id)
	Reason      string    `json:"reason"`      // 거부 사유 (admin_requests.reject_reason)
	CompletedAt time.Time `json:"completedAt"` // 엔진의 처리 완료 시각
}
