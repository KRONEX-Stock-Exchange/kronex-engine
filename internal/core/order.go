package core

import (
	"fmt"
	"log"
	"time"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

//////////////////////////////////////////////
// ----------------- Match ---------------- //
//////////////////////////////////////////////

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

// TODO: 미체결 지정가 주문시 UpdateHolding 이벤트가 발생하는 문제 수정하기
// CONSIDER: 자전거래 관련 로직 고려해보기
func (e *Engine) match(order domain.Order) error {
	events, err := e.matchEvents(order)
	if err != nil {
		return err
	}
	if err := e.appendOutput(events...); err != nil {
		panic(fmt.Errorf("engine: append output wal: %w", err))
	}
	return nil
}

// 매칭 후 이벤트 반환
func (e *Engine) matchEvents(order domain.Order) ([]outEvent, error) {
	ob := e.state.OrderBooks.Get(order.StockId)
	initialFilled := order.FilledQuantity
	remaining := order.Quantity - initialFilled

	// 영향 받은 호가창 기록
	type affectedLevel struct {
		side  domain.TradingType
		price uint64
	}
	var affectedLevels []affectedLevel
	seenLevel := make(map[affectedLevel]struct{})
	trackLevel := func(side domain.TradingType, price uint64) {
		level := affectedLevel{side: side, price: price}
		if _, ok := seenLevel[level]; ok {
			return
		}
		seenLevel[level] = struct{}{}
		affectedLevels = append(affectedLevels, level)
	}

	// 가용 잔고 및 수량 차감
	switch order.TradingType {
	case domain.TRADING_BUY:
		if acc, ok := e.state.Accounts.Get(order.AccountId); ok {
			acc.AvailableBalance -= order.Price * remaining
			e.state.Accounts.Upsert(&acc)
		}
	case domain.TRADING_SELL:
		if holding, ok := e.state.StockBalances.Get(order.AccountId, order.StockId); ok {
			holding.AvailableQuantity -= remaining
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
		trackLevel(counterSide, bestPrice)

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

		// Replay시 TradeID 중복 소비 방지
		tradeID := int64(0)
		if e.outputAppliedSeq == 0 || e.inputSeq > e.outputAppliedSeq {
			nextTradeID, err := e.nextTradeID()
			if err != nil {
				return nil, err
			}
			tradeID = nextTradeID
		}

		// 체결 내역
		events = append(events, outEvent{PatternTradeExecuted, domain.Trade{
			Id:           tradeID,
			StockId:      order.StockId,
			Price:        bestPrice,
			Quantity:     filled,
			MakerOrderId: counter.Id,
			TakerOrderId: order.Id,
			TradingType:  order.TradingType,
			ExecutedAt:   time.Now().UTC(),
		}})

		// Maker 주문 상태 (전량 체결 FILLED / 일부 체결 후 잔류 OPEN)
		makerFilled := counter.FilledQuantity + filled
		makerPattern := PatternOrderOpen
		if makerFilled == counter.Quantity {
			makerPattern = PatternOrderFilled
		}
		makerEvent := orderEvent(counter)
		makerEvent.FilledQuantity = makerFilled
		events = append(events, outEvent{makerPattern, makerEvent})

		// Maker 잔고
		trackAccount(counter.AccountId)
	}

	// 지정가 미체결분 호가창 등록
	if order.FilledQuantity < order.Quantity && order.OrderType == domain.ORDER_LIMIT {
		ob.Add(order)
		trackLevel(order.TradingType, order.Price)
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
		newlyFilled := order.FilledQuantity - initialFilled
		locked := order.Price * newlyFilled
		if refund := locked - filledCash; refund > 0 {
			if acc, ok := e.state.Accounts.Get(order.AccountId); ok {
				acc.AvailableBalance += refund
				e.state.Accounts.Upsert(&acc)
			}
		}
	}

	// 종목 현재가를 마지막 체결가로 갱신
	// NOTE: API Server와 락 순서 통일을 위해 DB 업데이트는 Account 업데이트 이후에 진행 (375L)
	var updatedStock *domain.Stock
	if lastTradePrice > 0 {
		if stock, ok := e.state.Stocks.Get(order.StockId); ok {
			stock.Price = lastTradePrice
			e.state.Stocks.Upsert(&stock)
			updatedStock = &stock
		}
	}

	// 내 주문(taker) 최종 상태
	events = append(events, outEvent{orderStatusPattern(order), orderEvent(order)})

	// 영향 받은 호가 최종 상태
	if len(affectedLevels) > 0 {
		levels := make([]domain.OrderBookLevel, 0, len(affectedLevels))
		for _, level := range affectedLevels {
			levels = append(levels, domain.OrderBookLevel{
				Side:     orderBookSide(level.side),
				Price:    level.price,
				Quantity: ob.LevelQuantity(level.side, level.price),
			})
		}
		events = append(events, outEvent{PatternOrderBookUpdated, domain.OrderBookUpdated{
			StockId: order.StockId,
			Levels:  levels,
		}})
	}

	// 잔고 변동 계좌 + 보유종목 최종 상태
	for _, id := range accountIDs {
		if acc, ok := e.state.Accounts.Get(id); ok {
			events = append(events, outEvent{PatternAccountUpdated, acc})
		}
		if holding, ok := e.state.StockBalances.Get(id, order.StockId); ok {
			events = append(events, outEvent{PatternHoldingUpdated, holding})
		}
	}

	if updatedStock != nil {
		events = append(events, outEvent{PatternStockUpdated, *updatedStock})
	}

	return events, nil
}

// 활성 주문 호가창에서 제거 및 예약 자산 반환
func (e *Engine) removeAndRelease(target domain.Order) bool {
	ob := e.state.OrderBooks.Get(target.StockId)
	// 주문 취소
	if !ob.Cancel(target.Id) {
		return false
	}

	// 예약 자산 반환
	remaining := target.Quantity - target.FilledQuantity
	switch target.TradingType {
	case domain.TRADING_BUY:
		if account, ok := e.state.Accounts.Get(target.AccountId); ok {
			account.AvailableBalance += target.Price * remaining
			e.state.Accounts.Upsert(&account)
		}
	case domain.TRADING_SELL:
		if holding, ok := e.state.StockBalances.Get(target.AccountId, target.StockId); ok {
			holding.AvailableQuantity += remaining
			e.state.StockBalances.Upsert(&holding)
		}
	}

	return true
}

// 주문 정정
func (e *Engine) edit(order domain.Order) error {
	log.Printf("engine: edit order id=%d", order.Id)

	ob := e.state.OrderBooks.Get(order.StockId)
	target, _ := ob.Get(order.TargetId)
	if !e.removeAndRelease(target) {
		return fmt.Errorf("remove edit target order %d", order.TargetId)
	}

	// NOTE: 주문 정정은 가격 변경만 허용
	replacement := order
	replacement.Quantity = target.Quantity
	replacement.FilledQuantity = target.FilledQuantity
	replacement.OrderType = target.OrderType
	replacement.TradingType = target.TradingType

	events, err := e.matchEvents(replacement)
	if err != nil {
		return err
	}

	// 매칭 후 나온 이벤트와 기존 Target Order 호가 업데이트 이벤트와 병합
	events = mergeOrderBookLevel(events, domain.OrderBookLevel{
		Side:     orderBookSide(target.TradingType),
		Price:    target.Price,
		Quantity: ob.LevelQuantity(target.TradingType, target.Price),
	}, target.StockId)

	// 기존 주문 상태 Replaced 변경 이벤트 생성
	events = append([]outEvent{{PatternOrderReplaced, orderEvent(target)}}, events...)

	// Output 생성
	if err := e.appendOutput(events...); err != nil {
		panic(fmt.Errorf("engine: append edit output wal: %w", err))
	}

	return nil
}

func mergeOrderBookLevel(events []outEvent, level domain.OrderBookLevel, stockID int32) []outEvent {
	for i := range events {
		if events[i].pattern != PatternOrderBookUpdated {
			continue
		}
		update, ok := events[i].data.(domain.OrderBookUpdated)
		if !ok {
			continue
		}

		update.Levels = append(update.Levels, level)
		events[i].data = update

		return events
	}

	return append(events, outEvent{PatternOrderBookUpdated, domain.OrderBookUpdated{
		StockId: stockID,
		Levels:  []domain.OrderBookLevel{level},
	}})
}

// 주문 취소
func (e *Engine) cancel(order domain.Order) error {
	log.Printf("engine: cancel order id=%d", order.Id)

	ob := e.state.OrderBooks.Get(order.StockId)
	target, ok := ob.Get(order.TargetId)
	if !ok {
		return fmt.Errorf("cancel target order %d is not active", order.TargetId)
	}
	if !e.removeAndRelease(target) {
		return fmt.Errorf("remove cancel target order %d", order.TargetId)
	}

	events := []outEvent{
		{PatternOrderCanceled, orderEvent(target)},
		{PatternOrderCompleted, orderEvent(order)},
		{PatternOrderBookUpdated, domain.OrderBookUpdated{
			StockId: target.StockId,
			Levels: []domain.OrderBookLevel{{
				Side:     orderBookSide(target.TradingType),
				Price:    target.Price,
				Quantity: ob.LevelQuantity(target.TradingType, target.Price),
			}},
		}},
	}

	// 복구가 끝난 최종 잔고/보유수량 발행
	switch target.TradingType {
	case domain.TRADING_BUY:
		if account, exists := e.state.Accounts.Get(target.AccountId); exists {
			events = append(events, outEvent{PatternAccountUpdated, account})
		}
	case domain.TRADING_SELL:
		if holding, exists := e.state.StockBalances.Get(target.AccountId, target.StockId); exists {
			events = append(events, outEvent{PatternHoldingUpdated, holding})
		}
	}

	if err := e.appendOutput(events...); err != nil {
		panic(fmt.Errorf("engine: append cancel output wal: %w", err))
	}

	return nil
}

//////////////////////////////////////////////
// ------------------ Util ---------------- //
//////////////////////////////////////////////

// 주문 이벤트 형태 변환용
func orderEvent(order domain.Order) domain.OrderEvent {
	return domain.OrderEvent{
		Id:             order.Id,
		TargetId:       order.TargetId,
		AccountId:      order.AccountId,
		StockId:        order.StockId,
		Price:          order.Price,
		TradingType:    order.TradingType,
		Quantity:       order.Quantity,
		FilledQuantity: order.FilledQuantity,
	}
}

func orderBookSide(side domain.TradingType) string {
	if side == domain.TRADING_SELL {
		return "SELL"
	}
	return "BUY"
}
