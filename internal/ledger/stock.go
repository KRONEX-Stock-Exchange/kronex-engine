package ledger

import (
	"slices"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

type Stocks struct {
	byID map[int32]*domain.Stock
}

func NewStocks() *Stocks {
	return &Stocks{byID: make(map[int32]*domain.Stock)}
}

// 종목 조회 (읽기 전용 복사본)
func (s *Stocks) Get(id int32) (domain.Stock, bool) {
	st, ok := s.byID[id]
	if !ok {
		return domain.Stock{}, false
	}
	return *st, true
}

func (s *Stocks) Upsert(st *domain.Stock) {
	s.byID[st.Id] = st
}

func (s *Stocks) toProto() []*ledgerpb.Stock {
	ids := make([]int32, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]*ledgerpb.Stock, 0, len(ids))
	for _, id := range ids {
		st := s.byID[id]
		out = append(out, &ledgerpb.Stock{
			Id:     st.Id,
			Price:  st.Price,
			Status: uint32(st.Status),
		})
	}
	return out
}

func (s *Stocks) fromProto(items []*ledgerpb.Stock) error {
	s.byID = make(map[int32]*domain.Stock, len(items))
	for _, pb := range items {
		s.byID[pb.Id] = &domain.Stock{
			Id:     pb.Id,
			Price:  pb.Price,
			Status: domain.StockStatus(pb.Status),
		}
	}
	return nil
}
