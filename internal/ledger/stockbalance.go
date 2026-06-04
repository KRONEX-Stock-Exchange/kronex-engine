package ledger

import (
	"cmp"
	"slices"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

type stockKey struct {
	accountID int32
	stockID   int32
}

type StockBalances struct {
	byKey map[stockKey]*domain.StockBalance
}

func NewStockBalances() *StockBalances {
	return &StockBalances{byKey: make(map[stockKey]*domain.StockBalance)}
}

func (s *StockBalances) Get(accountID, stockID int32) (*domain.StockBalance, bool) {
	sb, ok := s.byKey[stockKey{accountID, stockID}]
	return sb, ok
}

func (s *StockBalances) Upsert(sb *domain.StockBalance) {
	s.byKey[stockKey{sb.AccountId, sb.StockId}] = sb
}

func (s *StockBalances) toProto() []*ledgerpb.StockBalance {
	keys := make([]stockKey, 0, len(s.byKey))
	for k := range s.byKey {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b stockKey) int {
		// 계좌 번호순 / 동점시 종목번호 빠른순
		if a.accountID != b.accountID {
			return cmp.Compare(a.accountID, b.accountID)
		}
		return cmp.Compare(a.stockID, b.stockID)
	})

	out := make([]*ledgerpb.StockBalance, 0, len(keys))
	for _, k := range keys {
		sb := s.byKey[k]
		out = append(out, &ledgerpb.StockBalance{
			AccountId:         sb.AccountId,
			StockId:           sb.StockId,
			Quantity:          sb.Quantity,
			AvailableQuantity: sb.AvailableQuantity,
			Average:           sb.Average,
			TotalBuyAmount:    sb.TotalBuyAmount,
		})
	}
	return out
}

func (s *StockBalances) fromProto(items []*ledgerpb.StockBalance) error {
	s.byKey = make(map[stockKey]*domain.StockBalance, len(items))
	for _, pb := range items {
		s.byKey[stockKey{pb.AccountId, pb.StockId}] = &domain.StockBalance{
			AccountId:         pb.AccountId,
			StockId:           pb.StockId,
			Quantity:          pb.Quantity,
			AvailableQuantity: pb.AvailableQuantity,
			Average:           pb.Average,
			TotalBuyAmount:    pb.TotalBuyAmount,
		}
	}
	return nil
}
