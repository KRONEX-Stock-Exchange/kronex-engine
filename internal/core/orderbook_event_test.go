package core

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

func TestMatch_AppendsAffectedOrderBookLevels(t *testing.T) {
	output, err := wal.Open(filepath.Join(t.TempDir(), "output"), nil)
	if err != nil {
		t.Fatalf("open output WAL: %v", err)
	}
	t.Cleanup(func() { _ = output.Close() })

	e := &Engine{
		output:       output,
		state:        ledger.NewState(),
		inputSeq:     1,
		outputSignal: make(chan struct{}, 1),
	}
	e.state.Accounts.Upsert(&domain.Account{Id: 1, Balance: 10_000, AvailableBalance: 10_000})
	e.state.Accounts.Upsert(&domain.Account{Id: 2})
	e.state.Stocks.Upsert(&domain.Stock{Id: 1, Price: 99, Status: domain.LISTED})
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId: 2, StockId: 1, Quantity: 10, AvailableQuantity: 5,
		Average: 100, TotalBuyAmount: 1_000,
	})

	ob := e.state.OrderBooks.Get(1)
	ob.Add(domain.Order{Id: 10, AccountId: 2, StockId: 1, Price: 100, Quantity: 2, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_SELL})
	ob.Add(domain.Order{Id: 11, AccountId: 2, StockId: 1, Price: 101, Quantity: 3, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_SELL})

	if err := e.match(domain.Order{
		Id: 20, AccountId: 1, StockId: 1, Price: 101, Quantity: 10,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}); err != nil {
		t.Fatalf("match: %v", err)
	}

	raw, err := output.Read(1)
	if err != nil {
		t.Fatalf("read output WAL: %v", err)
	}
	var env OutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal output envelope: %v", err)
	}
	if env.OutputSeq != 1 {
		t.Fatalf("output seq = %d, want 1", env.OutputSeq)
	}

	var update domain.OrderBookUpdated
	found := false
	for _, event := range env.Events {
		if event.Pattern != PatternOrderBookUpdated {
			continue
		}
		found = true
		if err := json.Unmarshal(event.Data, &update); err != nil {
			t.Fatalf("unmarshal order book update: %v", err)
		}
	}
	if !found {
		t.Fatal("orderbook.updated event not found")
	}

	want := []domain.OrderBookLevel{
		{Side: "SELL", Price: 100, Quantity: 0},
		{Side: "SELL", Price: 101, Quantity: 0},
		{Side: "BUY", Price: 101, Quantity: 5},
	}
	if update.StockId != 1 {
		t.Errorf("stock ID = %d, want 1", update.StockId)
	}
	if len(update.Levels) != len(want) {
		t.Fatalf("levels = %+v, want %+v", update.Levels, want)
	}
	for i := range want {
		if update.Levels[i] != want[i] {
			t.Errorf("level[%d] = %+v, want %+v", i, update.Levels[i], want[i])
		}
	}
}
