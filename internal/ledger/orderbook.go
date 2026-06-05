package ledger

import (
	"container/list"
	"slices"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

//////////////////////////////////////////////
// -------------- OrderBook --------------- //
//////////////////////////////////////////////

// NOTE: FIFO 원칙을 지키기 위해 반듯이 PushBack, ReadFront 해야합니다.
// NOTE/TODO: 외부에서 buy, sell 필드를 직접 수정할 경우 정합성이 깨질 수 있어 비공개 필드로 전환 함
// 추후 매칭엔진에서 주문을 조회 하고 처리 할 수 있도록 하는 함수 추가 필요
type OrderBook struct {
	buy        map[uint64]*list.List // Price -> Order Nodes -> Order Node
	sell       map[uint64]*list.List
	buyPrices  []uint64            // 활성 매수 호가 (오름차순 정렬)
	sellPrices []uint64            // 활성 매도 호가 (오름차순 정렬)
	index      map[int64]*orderRef // Order Id -> Order Node
}

type orderRef struct {
	tradingType domain.TradingType
	price       uint64
	elem        *list.Element
}

func NewOrderBook() *OrderBook {
	return &OrderBook{
		buy:   make(map[uint64]*list.List),
		sell:  make(map[uint64]*list.List),
		index: make(map[int64]*orderRef),
	}
}

// NOTE: Order 복사로 Orderbook으로 소유권 이전
func (b *OrderBook) Add(order domain.Order) {
	side := b.buy
	prices := &b.buyPrices
	if order.TradingType == domain.TRADING_SELL {
		side = b.sell
		prices = &b.sellPrices
	}

	queue, ok := side[order.Price]
	if !ok {
		queue = list.New()
		side[order.Price] = queue
		// 새 가격대 생성 시 정렬 위치에 삽입
		i, _ := slices.BinarySearch(*prices, order.Price)
		*prices = slices.Insert(*prices, i, order.Price)
	}
	elem := queue.PushBack(&order)
	b.index[order.Id] = &orderRef{order.TradingType, order.Price, elem}
}

func (b *OrderBook) Cancel(orderId int64) bool {
	ref, ok := b.index[orderId]
	if !ok {
		return false // 이미 체결/취소됨
	}

	b.removeRef(orderId, ref)
	return true
}

// 최우선 매도 호가 조회 (가장 낮은 매도가) 호가창이 비었으면 ok=false
func (b *OrderBook) BestAsk() (price uint64, ok bool) {
	if len(b.sellPrices) == 0 {
		return 0, false
	}
	return b.sellPrices[0], true
}

// 최우선 매수 호가 조회 (가장 높은 매수가) 호가창이 비었으면 ok=false
func (b *OrderBook) BestBid() (price uint64, ok bool) {
	if len(b.buyPrices) == 0 {
		return 0, false
	}
	return b.buyPrices[len(b.buyPrices)-1], true
}

// 특정 호가에서 가장 우선순위가 높은(FIFO) 주문 반환 없으면 ok=false
func (b *OrderBook) Front(side domain.TradingType, price uint64) (order *domain.Order, ok bool) {
	queues := b.buy
	if side == domain.TRADING_SELL {
		queues = b.sell
	}

	queue, ok := queues[price]
	if !ok || queue.Len() == 0 {
		return nil, false
	}
	return queue.Front().Value.(*domain.Order), true
}

// 노드를 호가창에서 제거하고 index/가격 슬라이스를 동기화
// 빈 가격대는 가격 슬라이스에서도 함께 정리
func (b *OrderBook) removeRef(orderId int64, ref *orderRef) {
	side := b.buy
	prices := &b.buyPrices
	if ref.tradingType == domain.TRADING_SELL {
		side = b.sell
		prices = &b.sellPrices
	}

	queue := side[ref.price]
	queue.Remove(ref.elem)
	delete(b.index, orderId)

	// 빈 가격대 정리
	if queue.Len() == 0 {
		delete(side, ref.price)
		if i, found := slices.BinarySearch(*prices, ref.price); found {
			*prices = slices.Delete(*prices, i, i+1)
		}
	}
}

// 대기 주문을 want 만큼 체결 시도하고 실제 체결량을 반환
// NOTE:
// 남은 수량까지만 채우므로 want 가 더 커도 초과 체결되지 않음
// 전량 체결되면 호가창에서 제거 함. 주문이 없거나 잔량이 0이면 0을 반환
func (b *OrderBook) Fill(orderId int64, want uint64) (filled uint64) {
	ref, ok := b.index[orderId]
	if !ok {
		return 0 // 이미 체결/취소됨
	}

	order := ref.elem.Value.(*domain.Order)
	remaining := order.Quantity - order.FilledQuantity

	// 남은 수량까지만 클램프
	filled = min(want, remaining)
	if filled == 0 {
		return 0
	}

	order.FilledQuantity += filled

	// 전량 체결 시 호가창에서 제거
	if order.FilledQuantity == order.Quantity {
		b.removeRef(orderId, ref)
	}

	return filled
}

//////////////////////////////////////////////
// -------------- OrderBooks -------------- //
//////////////////////////////////////////////

type OrderBooks struct {
	byStock map[int32]*OrderBook
}

func NewOrderBooks() *OrderBooks {
	return &OrderBooks{byStock: make(map[int32]*OrderBook)}
}

func (b *OrderBooks) Get(stockId int32) *OrderBook {
	ob, ok := b.byStock[stockId]
	if !ok {
		ob = NewOrderBook()
		b.byStock[stockId] = ob
	}

	return ob
}

// OVERVIEW: 스냅샷을 찍기위한 Order 평탄화 함수
//
// NOTE: 매수 가격 오름차순, 매도 가격 오름차순으로 한 슬라이스에 모든 주문을 담습니다. (동일 가격에서는 우선 순위 높은 주문이 먼저)
//
// 만약 아래와 같은 상황일때 함수 내부 주석과 같이 동작합니다.
/*
	b.buy   (매수)
	├ 100원 → [id1] ⇄ [id2] (FIFO)
	└  99원 → [id3]
	b.sell  (매도)
	└ 105원 → [id4]

	Result -> [id3, id1, id2, id4]

	NOTE: id1, ... 는 실제 Order 객체
*/
func (b *OrderBook) orders() []*domain.Order {
	out := make([]*domain.Order, 0, len(b.index))

	sides := []struct {
		queues map[uint64]*list.List
		prices []uint64
	}{
		{b.buy, b.buyPrices},
		{b.sell, b.sellPrices},
	}

	for _, side := range sides {
		// out[id3, id1, id2]
		for _, p := range side.prices {
			for e := side.queues[p].Front(); e != nil; e = e.Next() {
				out = append(out, e.Value.(*domain.Order))
			}
		}

		// For문을 한번더 순회하여 매도가 추가되면 out[id3, id1, id2, id4]
	}

	return out
}

func (b *OrderBooks) toProto() []*ledgerpb.OrderBook {
	// Stock Map 평탄화
	ids := make([]int32, 0, len(b.byStock))
	for id := range b.byStock {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	// 각 Stock Orderbook 평탄화
	out := make([]*ledgerpb.OrderBook, 0, len(ids))
	for _, id := range ids {
		orders := b.byStock[id].orders()
		pbOrders := make([]*ledgerpb.Order, 0, len(orders))
		for _, o := range orders {
			pbOrders = append(pbOrders, &ledgerpb.Order{
				Id:             o.Id,
				TargetId:       o.TargetId,
				AccountId:      o.AccountId,
				StockId:        o.StockId,
				Price:          o.Price,
				Quantity:       o.Quantity,
				FilledQuantity: o.FilledQuantity,
				OrderType:      uint32(o.OrderType),
				TradingType:    uint32(o.TradingType),
			})
		}
		out = append(out, &ledgerpb.OrderBook{StockId: id, Orders: pbOrders})
	}
	return out
}

func (b *OrderBooks) fromProto(items []*ledgerpb.OrderBook) error {
	b.byStock = make(map[int32]*OrderBook, len(items))
	for _, pb := range items {
		ob := NewOrderBook()
		for _, po := range pb.Orders {
			// 호가창 재구성
			ob.Add(domain.Order{
				Id:             po.Id,
				TargetId:       po.TargetId,
				AccountId:      po.AccountId,
				StockId:        po.StockId,
				Price:          po.Price,
				Quantity:       po.Quantity,
				FilledQuantity: po.FilledQuantity,
				OrderType:      domain.OrderType(po.OrderType),
				TradingType:    domain.TradingType(po.TradingType),
			})
		}
		b.byStock[pb.StockId] = ob
	}
	return nil
}
