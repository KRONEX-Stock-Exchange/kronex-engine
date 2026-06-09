package core

import (
	"encoding/json"
	"fmt"
	"log"

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
	if err := validateOrder(order); err != nil {
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
// TODO: 원장 연결 후 실제 원장에 존재 하는지 추가해야됨
func validateOrder(order domain.Order) error {
	if order.Id <= 0 {
		return fmt.Errorf("invalid order id %d", order.Id)
	}

	switch order.TradingType {
	case domain.TRADING_BUY, domain.TRADING_SELL:
		if order.AccountId <= 0 {
			return fmt.Errorf("invalid account id %d", order.AccountId)
		}
		if order.StockId <= 0 {
			return fmt.Errorf("invalid stock id %d", order.StockId)
		}
		if order.Quantity == 0 {
			return fmt.Errorf("quantity must be greater than 0")
		}
	case domain.TRADING_EDIT, domain.TRADING_CANCEL:
		if order.TargetId <= 0 {
			return fmt.Errorf("invalid target id %d", order.TargetId)
		}
	default:
		return fmt.Errorf("unknown trading type %d", order.TradingType)
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

// TODO: 호가창 체결 로직 연결
func (e *Engine) match(order domain.Order) error {
	ob := e.state.OrderBooks.Get(order.StockId)

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

		// 체결 내역 발행
		trade := domain.Trade{
			StockId:      order.StockId,
			Price:        bestPrice,
			Quantity:     filled,
			MakerOrderId: counter.Id,
			TakerOrderId: order.Id,
		}
		payload, err := json.Marshal(trade)
		if err != nil {
			panic(fmt.Errorf("engine: marshal trade: %w", err))
		}
		if _, err := e.output.Append(payload); err != nil {
			panic(fmt.Errorf("engine: append trade to output wal: %w", err))
		}
	}

	// 잔량 호가창 등록
	if order.FilledQuantity < order.Quantity && order.OrderType == domain.ORDER_LIMIT {
		ob.Add(order)
	}

	// TODO: 시장가 일부 미체결일시 남은 잔량 모두 취소 처리
	if order.FilledQuantity != order.Quantity && order.OrderType == domain.ORDER_MARKET {

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
