package core

import (
	"errors"
	"fmt"
	"log"
	"math/bits"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

type RejectReason string

const (
	RejectInvalidOrder        RejectReason = "INVALID_ORDER"
	RejectInsufficientBalance RejectReason = "INSUFFICIENT_BALANCE"
	RejectInsufficientStock   RejectReason = "INSUFFICIENT_STOCK"
	RejectStockNotTradable    RejectReason = "STOCK_NOT_TRADABLE"
)

type RejectError struct {
	Reason RejectReason
	err    error
}

func (e *RejectError) Error() string { return e.err.Error() }
func (e *RejectError) Unwrap() error { return e.err }

func reject(reason RejectReason, format string, a ...any) *RejectError {
	return &RejectError{Reason: reason, err: fmt.Errorf(format, a...)}
}

// err에서 거부 사유 추출 (타입 불명 시 INVALID_ORDER)
func rejectReasonOf(err error) RejectReason {
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return RejectInvalidOrder
}

// 거부를 Output WAL에 기록 → publisher가 DB에 REJECTED 반영 및 이벤트 발행
func (e *Engine) appendReject(order domain.Order, err error) error {
	return e.appendOutput(outEvent{PatternOrderRejected, domain.OrderRejected{
		OrderId: order.Id,
		Reason:  string(rejectReasonOf(err)),
	}})
}

// 주문 유효성 검사
func (e *Engine) validateOrder(order domain.Order) error {
	if order.Id <= 0 {
		return reject(RejectInvalidOrder, "invalid order id %d", order.Id)
	}

	switch order.TradingType {
	case domain.TRADING_BUY, domain.TRADING_SELL:
		return e.validateTrade(order)
	case domain.TRADING_EDIT, domain.TRADING_CANCEL:
		if order.TargetId <= 0 {
			return reject(RejectInvalidOrder, "invalid target id %d", order.TargetId)
		}
		return nil
	default:
		return reject(RejectInvalidOrder, "unknown trading type %d", order.TradingType)
	}
}

// 원장 상태상 가능한 주문인지 검사
// NOTE: 시장가 주문시 주문가가 그날 상한으로 들어와야함
// CONSIDER: 상하한가 검사 추가
func (e *Engine) validateTrade(order domain.Order) error {
	if order.Quantity == 0 || order.FilledQuantity != 0 {
		return reject(RejectInvalidOrder, "invalid quantity (quantity=%d, filledQuantity=%d)", order.Quantity, order.FilledQuantity)
	}
	if order.OrderType != domain.ORDER_LIMIT && order.OrderType != domain.ORDER_MARKET {
		return reject(RejectInvalidOrder, "unsupported order type")
	}
	if order.Price == 0 {
		return reject(RejectInvalidOrder, "order price must be greater than 0")
	}

	// 원장 상태 존재 여부 검사
	account, ok := e.state.Accounts.Get(order.AccountId)
	if !ok {
		return reject(RejectInvalidOrder, "account %d does not exist", order.AccountId)
	}
	stock, ok := e.state.Stocks.Get(order.StockId)
	if !ok {
		return reject(RejectInvalidOrder, "stock %d does not exist", order.StockId)
	}

	// 거래 가능(상장) 상태인지
	if stock.Status != domain.LISTED {
		return reject(RejectStockNotTradable, "stock %d is not tradable (status=%d)", order.StockId, stock.Status)
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
		return reject(RejectInvalidOrder, "order cost overflow (price=%d qty=%d)", order.Price, order.Quantity)
	}
	if account.AvailableBalance < cost {
		return reject(RejectInsufficientBalance, "insufficient balance: need %d, available %d", cost, account.AvailableBalance)
	}

	return nil
}

// 매도 가능 수량 검사
func (e *Engine) validateSellable(order domain.Order) error {
	holding, ok := e.state.StockBalances.Get(order.AccountId, order.StockId)
	if !ok {
		return reject(RejectInsufficientStock, "account %d holds no stock %d", order.AccountId, order.StockId)
	}
	if holding.AvailableQuantity < order.Quantity {
		return reject(RejectInsufficientStock, "insufficient stock: have %d, want to sell %d", holding.AvailableQuantity, order.Quantity)
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

// 매칭 종료 후 들어온 주문의 결과 상태를 Output WAL 패턴으로 매핑한다
//   - 전량 체결         → order.filled
//   - 시장가 미체결 잔량  → order.canceled (호가창에 안 남음)
//   - 그 외(지정가 잔량)  → order.open     (호가창 등록)
func orderStatusPattern(order domain.Order) string {
	switch {
	case order.FilledQuantity == order.Quantity:
		return PatternOrderFilled
	case order.OrderType == domain.ORDER_MARKET:
		return PatternOrderCanceled
	default:
		return PatternOrderOpen
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
	var events []outEvent // Output WAL 이벤트 (체결마다 인라인 누적)

	// 잔고가 변동된 계좌 (체결 후 맨 마지막에 최종값만 이벤트로 발행)
	var accountIDs []int32
	seenAccount := make(map[int32]struct{})
	trackAccount := func(id int32) {
		if _, ok := seenAccount[id]; !ok {
			seenAccount[id] = struct{}{}
			accountIDs = append(accountIDs, id)
		}
	}

	// Tracker 잔고
	trackAccount(order.AccountId)

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

		// 체결 내역
		events = append(events, outEvent{PatternTradeExecuted, domain.Trade{
			StockId:      order.StockId,
			Price:        bestPrice,
			Quantity:     filled,
			MakerOrderId: counter.Id,
			TakerOrderId: order.Id,
		}})

		// Maker 주문 상태 (전량 체결 FILLED / 일부 체결 후 잔류 OPEN)
		makerFilled := counter.FilledQuantity + filled
		makerPattern := PatternOrderOpen
		if makerFilled == counter.Quantity {
			makerPattern = PatternOrderFilled
		}
		events = append(events, outEvent{makerPattern, domain.OrderEvent{
			OrderId:        counter.Id,
			AccountId:      counter.AccountId,
			StockId:        order.StockId,
			Price:          counter.Price,
			Quantity:       counter.Quantity,
			FilledQuantity: makerFilled,
		}})

		// Maker 잔고
		trackAccount(counter.AccountId)
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

	// 내 주문(taker) 최종 상태
	events = append(events, outEvent{orderStatusPattern(order), domain.OrderEvent{
		OrderId:        order.Id,
		AccountId:      order.AccountId,
		StockId:        order.StockId,
		Price:          order.Price,
		Quantity:       order.Quantity,
		FilledQuantity: order.FilledQuantity,
	}})

	// 잔고 변동 계좌 + 보유종목 최종 상태
	for _, id := range accountIDs {
		if acc, ok := e.state.Accounts.Get(id); ok {
			events = append(events, outEvent{PatternAccountUpdated, acc})
		}
		if holding, ok := e.state.StockBalances.Get(id, order.StockId); ok {
			events = append(events, outEvent{PatternHoldingUpdated, holding})
		}
	}

	// Output WAL 작성
	if err := e.appendOutput(events...); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}

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
