package domain

type StockStatus uint8

const (
	LISTED StockStatus = iota
	SUSPENDED
	DELISTED
)

type Stock struct {
	Id     int32
	Price  uint64
	Status StockStatus
}
