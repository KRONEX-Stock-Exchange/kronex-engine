package domain

type StockStatus uint8

const (
	StockListed StockStatus = iota
	StockSuspended
	StockDelisted
)

type Stock struct {
	Id     int32
	Price  uint64
	Status StockStatus
}
