package ledger

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

const snapshotVersion = 1

type State struct {
	Accounts      *Accounts
	StockBalances *StockBalances
	Stocks        *Stocks
	OrderBooks    *OrderBooks
}

func NewState() *State {
	return &State{
		Accounts:      NewAccounts(),
		StockBalances: NewStockBalances(),
		Stocks:        NewStocks(),
		OrderBooks:    NewOrderBooks(),
	}
}

// 전체 상태 직렬화
func (s *State) Serialize() ([]byte, error) {
	snap := &ledgerpb.LedgerSnapshot{
		Version:       snapshotVersion,
		Accounts:      s.Accounts.toProto(),
		StockBalances: s.StockBalances.toProto(),
		Stocks:        s.Stocks.toProto(),
		OrderBooks:    s.OrderBooks.toProto(),
	}

	data, err := proto.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal ledger snapshot: %w", err)
	}
	return data, nil
}

// 역직렬화
func (s *State) Restore(data []byte) error {
	var snap ledgerpb.LedgerSnapshot
	if err := proto.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("unmarshal ledger snapshot: %w", err)
	}

	if snap.Version != snapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d (want %d)", snap.Version, snapshotVersion)
	}

	if err := s.Accounts.fromProto(snap.Accounts); err != nil {
		return fmt.Errorf("restore accounts: %w", err)
	}
	if err := s.StockBalances.fromProto(snap.StockBalances); err != nil {
		return fmt.Errorf("restore stock balances: %w", err)
	}
	if err := s.Stocks.fromProto(snap.Stocks); err != nil {
		return fmt.Errorf("restore stocks: %w", err)
	}
	if err := s.OrderBooks.fromProto(snap.OrderBooks); err != nil {
		return fmt.Errorf("restore order books: %w", err)
	}
	return nil
}
