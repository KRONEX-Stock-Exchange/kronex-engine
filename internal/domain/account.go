package domain

type Account struct {
	Id               int32  `json:"id,string"`
	Balance          uint64 `json:"balance,string"`
	AvailableBalance uint64 `json:"availableBalance,string"`
}
