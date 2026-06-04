package ledger

import (
	"slices"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

type Accounts struct {
	byID map[int32]*domain.Account
}

func NewAccounts() *Accounts {
	return &Accounts{byID: make(map[int32]*domain.Account)}
}

func (a *Accounts) Get(id int32) (*domain.Account, bool) {
	acc, ok := a.byID[id]
	return acc, ok
}

func (a *Accounts) Upsert(acc *domain.Account) {
	a.byID[acc.Id] = acc
}

func (a *Accounts) toProto() []*ledgerpb.Account {
	ids := make([]int32, 0, len(a.byID))
	for id := range a.byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]*ledgerpb.Account, 0, len(ids))
	for _, id := range ids {
		acc := a.byID[id]
		out = append(out, &ledgerpb.Account{
			Id:               acc.Id,
			Balance:          acc.Balance,
			AvailableBalance: acc.AvailableBalance,
		})
	}
	return out
}

func (a *Accounts) fromProto(items []*ledgerpb.Account) error {
	a.byID = make(map[int32]*domain.Account, len(items))
	for _, pb := range items {
		a.byID[pb.Id] = &domain.Account{
			Id:               pb.Id,
			Balance:          pb.Balance,
			AvailableBalance: pb.AvailableBalance,
		}
	}
	return nil
}
