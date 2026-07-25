package core

import (
	"math/bits"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// 주문 유효성 검사
// TODO: 호가 단위 검사 추가 필요
func (e *Engine) validateOrder(order domain.Order) error {
	if order.Id <= 0 {
		return reject(RejectInvalidOrder, "invalid order id %d", order.Id)
	}
	if err := e.validateStock(order); err != nil {
		return err
	}

	switch order.TradingType {
	case domain.TRADING_BUY:
		return e.validateBuy(order)
	case domain.TRADING_SELL:
		return e.validateSell(order)
	case domain.TRADING_EDIT:
		return e.validateEdit(order)
	case domain.TRADING_CANCEL:
		return e.validateCancel(order)
	default:
		return reject(RejectInvalidOrder, "unknown trading type %d", order.TradingType)
	}
}

// Stock 존재 여부와 거래 가능 상태 검사
func (e *Engine) validateStock(order domain.Order) error {
	stock, ok := e.state.Stocks.Get(order.StockId)
	if !ok {
		return reject(RejectInvalidOrder, "stock %d does not exist", order.StockId)
	}

	// NOTE: 주식을 거래 할 수 없게 됬을때는 주문 취소만 가능
	if stock.Status != domain.LISTED && order.TradingType != domain.TRADING_CANCEL {
		return reject(RejectStockNotTradable, "stock %d is not tradable (status=%d)", order.StockId, stock.Status)
	}

	return nil
}

// 매수/매도 주문 공통 필드 검증
// NOTE: 시장가 매수 주문시 주문가가 그날 상한으로 들어와야함
// ㄴ CONSIDER: 추후 상한으로 들어오지 않아도 되도록 수정하기
// TODO: 상하한가 검사 추가
func validateTrade(order domain.Order) error {
	if order.Quantity == 0 || order.FilledQuantity != 0 {
		return reject(RejectInvalidOrder, "invalid quantity (quantity=%d, filledQuantity=%d)", order.Quantity, order.FilledQuantity)
	}
	if order.OrderType != domain.ORDER_LIMIT && order.OrderType != domain.ORDER_MARKET {
		return reject(RejectInvalidOrder, "unsupported order type")
	}
	if order.Price == 0 {
		return reject(RejectInvalidOrder, "order price must be greater than 0")
	}

	return nil
}

// 매수 주문 유효성 검사
func (e *Engine) validateBuy(order domain.Order) error {
	if err := validateTrade(order); err != nil {
		return err
	}
	account, ok := e.state.Accounts.Get(order.AccountId)
	if !ok {
		return reject(RejectInvalidOrder, "account %d does not exist", order.AccountId)
	}

	// uint64 Overflow 검사
	hi, cost := bits.Mul64(order.Price, order.Quantity)
	if hi != 0 {
		return reject(RejectInvalidOrder, "order cost overflow (price=%d qty=%d)", order.Price, order.Quantity)
	}

	if account.AvailableBalance < cost {
		return reject(RejectInsufficientBalance, "insufficient balance: need %d, available %d", cost, account.AvailableBalance)
	}

	return nil
}

// 매도 주문 유효성 검사
func (e *Engine) validateSell(order domain.Order) error {
	if err := validateTrade(order); err != nil {
		return err
	}

	holding, ok := e.state.StockBalances.Get(order.AccountId, order.StockId)
	if !ok {
		return reject(RejectInsufficientStock, "account %d holds no stock %d", order.AccountId, order.StockId)
	}
	if holding.AvailableQuantity < order.Quantity {
		return reject(RejectInsufficientStock, "insufficient stock: have %d, want to sell %d", holding.AvailableQuantity, order.Quantity)
	}

	return nil
}

func (e *Engine) validateEdit(order domain.Order) error {
	target, err := e.validateTargetRequest(order)
	if err != nil {
		return err
	}
	if order.Id == order.TargetId {
		return reject(RejectInvalidOrder, "edit order id must differ from target id %d", order.TargetId)
	}
	// NOTE: 동일 가격 주문 접수는 거부함
	if order.Price == 0 || order.Price == target.Price {
		return reject(RejectInvalidOrder, "invalid edit price %d", order.Price)
	}

	remaining := target.Quantity - target.FilledQuantity
	switch target.TradingType {
	case domain.TRADING_BUY:
		account, ok := e.state.Accounts.Get(target.AccountId)
		if !ok {
			return reject(RejectInvalidOrder, "account %d does not exist", target.AccountId)
		}

		oldHi, oldReserved := bits.Mul64(target.Price, remaining) // 기존 주문 예약 금액
		newHi, newRequired := bits.Mul64(order.Price, remaining)  // 가격이 변경된 주문의 예약 금액
		if oldHi != 0 || newHi != 0 {
			return reject(RejectInvalidOrder, "edit order cost overflow (price=%d qty=%d)", order.Price, remaining)
		}

		effective, carry := bits.Add64(account.AvailableBalance, oldReserved, 0)
		if carry != 0 {
			return reject(RejectInvalidOrder, "edit buying power overflow")
		}
		if effective < newRequired {
			return reject(RejectInsufficientBalance, "insufficient balance: need %d, available %d", newRequired, effective)
		}
	case domain.TRADING_SELL:
		if _, ok := e.state.StockBalances.Get(target.AccountId, target.StockId); !ok {
			return reject(RejectInsufficientStock, "account %d holds no stock %d", target.AccountId, target.StockId)
		}
	}

	return nil
}

func (e *Engine) validateCancel(order domain.Order) error {
	_, err := e.validateTargetRequest(order)
	return err
}

// 정정/취소 대상(Target) 주문 검증
func (e *Engine) validateTargetRequest(order domain.Order) (domain.Order, error) {
	if order.TargetId <= 0 {
		return domain.Order{}, reject(RejectInvalidOrder, "invalid target id %d", order.TargetId)
	}

	target, ok := e.state.OrderBooks.Get(order.StockId).Get(order.TargetId)
	if !ok || target.AccountId != order.AccountId {
		return domain.Order{}, reject(RejectOrderNotActive, "target order %d is not active", order.TargetId)
	}

	return target, nil
}
