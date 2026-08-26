package domain

import "time"

// 계좌 간 송금 요청 (transfer.created)
type Transfer struct {
	Id                 int64  `json:"id,string"`                 // 요청 고유 ID (멱등성)
	SenderAccountId    int32  `json:"senderAccountId,string"`    // 보내는 계좌
	RecipientAccountId int32  `json:"recipientAccountId,string"` // 받는 계좌
	Amount             uint64 `json:"amount,string"`             // 송금 금액
}

// 송금 완료 이벤트
type TransferCompleted struct {
	Id                 int64     `json:"id,string"`
	SenderAccountId    int32     `json:"senderAccountId,string"`
	RecipientAccountId int32     `json:"recipientAccountId,string"`
	Amount             uint64    `json:"amount,string"`
	CompletedAt        time.Time `json:"completedAt"` // 처리 완료 시각
}

// 유효성 검사 실패로 거부된 송금 이벤트
type TransferRejected struct {
	Id                 int64     `json:"id,string"`
	SenderAccountId    int32     `json:"senderAccountId,string"`
	RecipientAccountId int32     `json:"recipientAccountId,string"`
	Amount             uint64    `json:"amount,string"`
	Reason             string    `json:"reason"`
	CompletedAt        time.Time `json:"completedAt"` // 처리 완료 시각
}
