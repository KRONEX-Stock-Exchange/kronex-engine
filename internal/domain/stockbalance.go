package domain

type StockBalance struct {
	AccountId         int32  `json:"accountId,string"`
	StockId           int32  `json:"stockId,string"`
	Quantity          uint64 `json:"quantity,string"`
	AvailableQuantity uint64 `json:"availableQuantity,string"`
	Average           uint64 `json:"average,string"`
	TotalBuyAmount    uint64 `json:"totalBuyAmount,string"`
}

type StockBalanceAdjust struct {
	Id        int64  `json:"id,string"`        // 요청 고유 ID (멱등성)
	AccountId int32  `json:"accountId,string"` // 대상 계좌
	StockId   int32  `json:"stockId,string"`   // 대상 종목
	Delta     int64  `json:"delta,string"`     // 양수: 증가, 음수: 감소
	Average   uint64 `json:"average,string"`   // 최초 보유 생성 시 평균 단가
}
