package domain

type Account struct {
	Id               int32  `json:"id,string"`
	Balance          uint64 `json:"balance,string"`
	AvailableBalance uint64 `json:"availableBalance,string"`
}

type BalanceAdjust struct {
	Id        int64  `json:"id,string"`        // 요청 고유 ID (멱등성)
	AccountId int32  `json:"accountId,string"` // 대상 계좌
	Delta     int64  `json:"delta,string"`     // 양수: 증가, 음수: 감소
}
