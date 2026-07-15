// 테스트 항목:
// - 모든 주문 유형에서 존재하지 않는 종목을 루트 공통 검증으로 거부
// - 모든 주문 유형에서 거래 불가능한 종목을 루트 공통 검증으로 거부
// - 매수 주문과 매수 정정에서 필요한 계좌를 해당 검증 함수에서 조회
package core

import (
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
)

func TestValidateOrderAppliesCommonStockChecks(t *testing.T) {
	orderTypes := []struct {
		name        string
		tradingType domain.TradingType
	}{
		{name: "buy", tradingType: domain.TRADING_BUY},
		{name: "sell", tradingType: domain.TRADING_SELL},
		{name: "edit", tradingType: domain.TRADING_EDIT},
		{name: "cancel", tradingType: domain.TRADING_CANCEL},
	}
	checks := []struct {
		name string
		seed func(*Engine)
		want RejectReason
	}{
		{
			name: "missing stock",
			seed: func(*Engine) {},
			want: RejectInvalidOrder,
		},
		{
			name: "stock not tradable",
			seed: func(e *Engine) {
				e.state.Stocks.Upsert(&domain.Stock{Id: 1, Status: domain.SUSPENDED})
			},
			want: RejectStockNotTradable,
		},
	}

	for _, orderType := range orderTypes {
		for _, check := range checks {
			t.Run(orderType.name+"/"+check.name, func(t *testing.T) {
				e := &Engine{state: ledger.NewState()}
				check.seed(e)
				order := domain.Order{
					Id: 1, TargetId: 10, AccountId: 1, StockId: 1,
					Price: 100, Quantity: 1,
					OrderType: domain.ORDER_LIMIT, TradingType: orderType.tradingType,
				}
				if orderType.tradingType == domain.TRADING_EDIT || orderType.tradingType == domain.TRADING_CANCEL {
					order.Quantity = 0
				}

				err := e.validateOrder(order)
				if err == nil {
					t.Fatal("validateOrder accepted invalid stock")
				}
				if got := rejectReasonOf(err); got != check.want {
					t.Errorf("reject reason = %s, want %s", got, check.want)
				}
			})
		}
	}
}

func TestValidateBuyChecksRequiredAccount(t *testing.T) {
	e := &Engine{state: ledger.NewState()}
	e.state.Stocks.Upsert(&domain.Stock{Id: 1, Status: domain.LISTED})
	order := domain.Order{
		Id: 1, AccountId: 1, StockId: 1, Price: 100, Quantity: 1,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}

	err := e.validateOrder(order)
	if err == nil {
		t.Fatal("validateOrder accepted buy order without account")
	}
	if got := rejectReasonOf(err); got != RejectInvalidOrder {
		t.Errorf("reject reason = %s, want %s", got, RejectInvalidOrder)
	}
}

func TestValidateEditBuyChecksRequiredAccount(t *testing.T) {
	e := &Engine{state: ledger.NewState()}
	e.state.Stocks.Upsert(&domain.Stock{Id: 1, Status: domain.LISTED})
	target := domain.Order{
		Id: 10, AccountId: 1, StockId: 1, Price: 100, Quantity: 10,
		OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_BUY,
	}
	e.state.OrderBooks.Get(1).Add(target)
	request := domain.Order{
		Id: 20, TargetId: target.Id, AccountId: target.AccountId, StockId: target.StockId,
		Price: 90, OrderType: domain.ORDER_LIMIT, TradingType: domain.TRADING_EDIT,
	}

	err := e.validateOrder(request)
	if err == nil {
		t.Fatal("validateOrder accepted buy edit without account")
	}
	if got := rejectReasonOf(err); got != RejectInvalidOrder {
		t.Errorf("reject reason = %s, want %s", got, RejectInvalidOrder)
	}
}
