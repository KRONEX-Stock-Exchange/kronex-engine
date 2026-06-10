package core

import (
	"encoding/json"
	"fmt"
	"log"
	"math/bits"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func (e *Engine) handleOrder(d Delivery, data json.RawMessage) error {
	var order domain.Order
	if err := json.Unmarshal(data, &order); err != nil {
		log.Printf("engine: decode order: %v", err)
		return d.Nack(false)
	}
	log.Printf("engine: received order %+v", order)

	// 유효성 검사
	if err := e.validateOrder(order); err != nil {
		log.Printf("engine: invalid order %d: %v", order.Id, err)

		// TODO: 주문 현황을 업데이트 하는 별도 DB Publisher 필요
		return d.Nack(false)
	}

	// 주문 처리
	if err := e.route(order); err != nil {
		log.Printf("engine: route order %d: %v", order.Id, err)

		return err
		// NOTE: 추후 자전거래 방지와 같은 별도 에러가 던져질 경우에는 Nack 처리가 필요함
		// return d.Nack(false)
	}

	return d.Ack()
}

// 주문 유효성 검사
func (e *Engine) validateOrder(order domain.Order) error {
	if order.Id <= 0 {
		return fmt.Errorf("invalid order id %d", order.Id)
	}

	switch order.TradingType {
	case domain.TRADING_BUY, domain.TRADING_SELL:
		return e.validateTrade(order)
	case domain.TRADING_EDIT, domain.TRADING_CANCEL:
		if order.TargetId <= 0 {
			return fmt.Errorf("invalid target id %d", order.TargetId)
		}
		return nil
	default:
		return fmt.Errorf("unknown trading type %d", order.TradingType)
	}
}

// 원장 상태상 가능한 주문인지 검사
// NOTE: 시장가 주문시 주문가가 그날 상한으로 들어와야함
// CONSIDER: 상하한가 검사 추가
func (e *Engine) validateTrade(order domain.Order) error {
	if order.Quantity == 0 || order.FilledQuantity != 0 {
		return fmt.Errorf("invalid quantity (quantity=%d, filledQuantity=%d)", order.Quantity, order.FilledQuantity)
	}
	if order.OrderType != domain.ORDER_LIMIT && order.OrderType != domain.ORDER_MARKET {
		return fmt.Errorf("unsupported order type")
	}
	if order.Price == 0 {
		return fmt.Errorf("rder price must be greater than 0")
	}

	// 원장 상태 존재 여부 검사
	account, ok := e.state.Accounts.Get(order.AccountId)
	if !ok {
		return fmt.Errorf("account %d does not exist", order.AccountId)
	}
	stock, ok := e.state.Stocks.Get(order.StockId)
	if !ok {
		return fmt.Errorf("stock %d does not exist", order.StockId)
	}

	// 거래 가능(상장) 상태인지
	if stock.Status != domain.LISTED {
		return fmt.Errorf("stock %d is not tradable (status=%d)", order.StockId, stock.Status)
	}

	switch order.TradingType {
	case domain.TRADING_BUY:
		return validateBuyingPower(order, account)
	case domain.TRADING_SELL:
		return e.validateSellable(order)
	}
	return nil
}

// 매수 가능 금액 검사
func validateBuyingPower(order domain.Order, account domain.Account) error {
	hi, cost := bits.Mul64(order.Price, order.Quantity)
	if hi != 0 {
		return fmt.Errorf("order cost overflow (price=%d qty=%d)", order.Price, order.Quantity)
	}
	if account.AvailableBalance < cost {
		return fmt.Errorf("insufficient balance: need %d, available %d", cost, account.AvailableBalance)
	}

	return nil
}

// 매도 가능 수량 검사
func (e *Engine) validateSellable(order domain.Order) error {
	holding, ok := e.state.StockBalances.Get(order.AccountId, order.StockId)
	if !ok {
		return fmt.Errorf("account %d holds no stock %d", order.AccountId, order.StockId)
	}
	if holding.AvailableQuantity < order.Quantity {
		return fmt.Errorf("insufficient stock: have %d, want to sell %d", holding.AvailableQuantity, order.Quantity)
	}
	return nil
}

func (e *Engine) route(order domain.Order) error {
	switch order.TradingType {
	case domain.TRADING_BUY, domain.TRADING_SELL:
		return e.match(order)
	case domain.TRADING_EDIT:
		return e.edit(order)
	case domain.TRADING_CANCEL:
		return e.cancel(order)
	default:
		return fmt.Errorf("unknown trading type %d", order.TradingType)
	}
}

// CONSIDER: 자전거래 관련 로직 고려해보기
func (e *Engine) match(order domain.Order) error {
	ob := e.state.OrderBooks.Get(order.StockId)

	// 가용 잔고 및 수량 차감
	switch order.TradingType {
	case domain.TRADING_BUY:
		if acc, ok := e.state.Accounts.Get(order.AccountId); ok {
			acc.AvailableBalance -= order.Price * order.Quantity
			e.state.Accounts.Upsert(&acc)
		}
	case domain.TRADING_SELL:
		if holding, ok := e.state.StockBalances.Get(order.AccountId, order.StockId); ok {
			holding.AvailableQuantity -= order.Quantity
			e.state.StockBalances.Upsert(&holding)
		}
	}

	var filledCash uint64 // 총 실 체결 금액
	var lastTradePrice uint64

	// 모든 수량이 소진 될때까지 반복
	for order.FilledQuantity < order.Quantity {
		var bestPrice uint64
		var ok bool
		var counterSide domain.TradingType

		switch order.TradingType {
		case domain.TRADING_BUY:
			bestPrice, ok = ob.BestAsk()
			counterSide = domain.TRADING_SELL
		case domain.TRADING_SELL:
			bestPrice, ok = ob.BestBid()
			counterSide = domain.TRADING_BUY
		}

		// 더 이상 체결할 주문이 없을 경우 종료
		if !ok {
			break
		}
		// 지정가일 경우에 제출 가격을 기준으로 만족하는지 검사
		if order.OrderType == domain.ORDER_LIMIT {
			if order.TradingType == domain.TRADING_BUY && bestPrice > order.Price {
				break
			}
			if order.TradingType == domain.TRADING_SELL && bestPrice < order.Price {
				break
			}
		}

		// 해당 호가에서 우선순위가 가장 높은 주문 조회
		counter, ok := ob.Front(counterSide, bestPrice)
		if !ok {
			// 호가가 비어있으면 활성화 호가 배열에 존재하면 안됨 (존재 하지 않는 경우의 수)
			break
		}

		// 체결 처리
		want := order.Quantity - order.FilledQuantity
		filled := ob.Fill(counter.Id, want)
		if filled == 0 {
			// 이미 상대 주문이 처리된 주문일 경우 (존재 하지 않는 경우의 수)
			break
		}
		order.FilledQuantity += filled

		// 원장 상태 업데이트
		cash := bestPrice * filled
		filledCash += cash
		lastTradePrice = bestPrice
		buyerID, sellerID := order.AccountId, counter.AccountId
		if order.TradingType == domain.TRADING_SELL {
			buyerID, sellerID = counter.AccountId, order.AccountId
		}

		// 매수자 잔고 차감 및 주식 입고
		if buyer, ok := e.state.Accounts.Get(buyerID); ok {
			buyer.Balance -= cash
			e.state.Accounts.Upsert(&buyer)
		}
		buyerHold, exists := e.state.StockBalances.Get(buyerID, order.StockId)
		if !exists {
			buyerHold = domain.StockBalance{AccountId: buyerID, StockId: order.StockId}
		}

		buyerHold.Quantity += filled
		buyerHold.AvailableQuantity += filled
		buyerHold.TotalBuyAmount += cash
		buyerHold.Average = buyerHold.TotalBuyAmount / buyerHold.Quantity
		e.state.StockBalances.Upsert(&buyerHold)

		// 매도자 잔고 증감 및 주식 출고
		if seller, ok := e.state.Accounts.Get(sellerID); ok {
			seller.Balance += cash
			seller.AvailableBalance += cash
			e.state.Accounts.Upsert(&seller)
		}
		if sellerHold, ok := e.state.StockBalances.Get(sellerID, order.StockId); ok {
			sellerHold.Quantity -= filled
			sellerHold.TotalBuyAmount -= filled * sellerHold.Average
			if sellerHold.Quantity == 0 {
				sellerHold.Average = 0
				sellerHold.TotalBuyAmount = 0
			}
			e.state.StockBalances.Upsert(&sellerHold)
		}

		// TODO: 모두 체결되고 마지막에 한번에 작성하도록 해야함
		// 체결 내역 발행 (Out WAL 작성)
		trade := domain.Trade{
			StockId:      order.StockId,
			Price:        bestPrice,
			Quantity:     filled,
			MakerOrderId: counter.Id,
			TakerOrderId: order.Id,
		}
		if err := e.appendOutput(PatternTradeExecuted, trade); err != nil {
			panic(fmt.Errorf("engine: append trade to output wal: %w", err))
		}
	}

	// 지정가 미체결분 호가창 등록
	if order.FilledQuantity < order.Quantity && order.OrderType == domain.ORDER_LIMIT {
		ob.Add(order)
	}

	// 시장가 미체결분 취소 및 가용 잔고 복구
	if order.FilledQuantity < order.Quantity && order.OrderType == domain.ORDER_MARKET {
		unfilled := order.Quantity - order.FilledQuantity
		switch order.TradingType {
		case domain.TRADING_BUY:
			if acc, ok := e.state.Accounts.Get(order.AccountId); ok {
				acc.AvailableBalance += order.Price * unfilled
				e.state.Accounts.Upsert(&acc)
			}
		case domain.TRADING_SELL:
			if holding, ok := e.state.StockBalances.Get(order.AccountId, order.StockId); ok {
				holding.AvailableQuantity += unfilled
				e.state.StockBalances.Upsert(&holding)
			}
		}
	}

	// 잠근 매수 금액과 실제 체결 금액과의 오차 보정
	if order.TradingType == domain.TRADING_BUY {
		locked := order.Price * order.FilledQuantity
		if refund := locked - filledCash; refund > 0 {
			if acc, ok := e.state.Accounts.Get(order.AccountId); ok {
				acc.AvailableBalance += refund
				e.state.Accounts.Upsert(&acc)
			}
		}
	}

	// 종목 현재가를 마지막 체결가로 갱신
	if lastTradePrice > 0 {
		if stock, ok := e.state.Stocks.Get(order.StockId); ok {
			stock.Price = lastTradePrice
			e.state.Stocks.Upsert(&stock)
		}
	}

	// 체결 후 호가창 상태 출력 (최우선호가 ±10단계)
	log.Printf("orderbook stock=%d\n%s", order.StockId, ob.Render(10))

	return nil
}

// TODO: 주문 정정 로직 연결
func (e *Engine) edit(order domain.Order) error {
	log.Printf("engine: edit order id=%d", order.Id)
	return nil
}

// TODO: 주문 취소 로직 연결
func (e *Engine) cancel(order domain.Order) error {
	log.Printf("engine: cancel order id=%d", order.Id)
	return nil
}
