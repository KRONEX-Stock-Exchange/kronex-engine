package domain

import (
	"encoding/json"
	"fmt"
)

type StockStatus uint8

const (
	LISTED StockStatus = iota
	SUSPENDED
	DELISTED
	PENDING
)

func (s StockStatus) String() string {
	switch s {
	case SUSPENDED:
		return "SUSPENDED"
	case DELISTED:
		return "DELISTED"
	case PENDING:
		return "PENDING"
	default:
		return "LISTED"
	}
}

func (s StockStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

func (s *StockStatus) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	switch str {
	case "LISTED":
		*s = LISTED
	case "SUSPENDED":
		*s = SUSPENDED
	case "DELISTED":
		*s = DELISTED
	case "PENDING":
		*s = PENDING
	default:
		return fmt.Errorf("invalid stock status: %q", str)
	}
	return nil
}

type Stock struct {
	Id     int32       `json:"id"`
	Price  uint64      `json:"price,string"`
	Status StockStatus `json:"status"`
}
