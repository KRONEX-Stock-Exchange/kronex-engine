package ledger

import (
	"container/list"
	"slices"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

// NOTE: FIFO 원칙을 지키기 위해 반듯이 PushBack, ReadFront 해야합니다.
type OrderBook struct {
	Buy   map[uint64]*list.List // Price -> Order Nodes -> Order Node
	Sell  map[uint64]*list.List
	index map[int64]*orderRef // Order Id -> Order Node
}

type orderRef struct {
	tradingType domain.TradingType
	price       uint64
	elem        *list.Element
}

func NewOrderBook() *OrderBook {
	return &OrderBook{
		Buy:   make(map[uint64]*list.List),
		Sell:  make(map[uint64]*list.List),
		index: make(map[int64]*orderRef),
	}
}

// NOTE: Order 복사로 Orderbook으로 소유권 이전
func (b *OrderBook) Add(order domain.Order) {
	side := b.Buy
	if order.TradingType == domain.TRADING_SELL {
		side = b.Sell
	}

	queue, ok := side[order.Price]
	if !ok {
		queue = list.New()
		side[order.Price] = queue
	}
	elem := queue.PushBack(&order)
	b.index[order.Id] = &orderRef{order.TradingType, order.Price, elem}
}

func (b *OrderBook) Cancel(orderId int64) bool {
	ref, ok := b.index[orderId]
	if !ok {
		return false // 이미 체결/취소됨
	}
	side := b.Buy
	if ref.tradingType == domain.TRADING_SELL {
		side = b.Sell
	}

	// 주문삭제
	queue := side[ref.price]
	queue.Remove(ref.elem)
	delete(b.index, orderId)

	// 빈 가격대 정리
	if queue.Len() == 0 {
		delete(side, ref.price)
	}

	return true
}

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
	b.Buy   (매수)
	├ 100원 → [id1] ⇄ [id2] (FIFO)
	└  99원 → [id3]
	b.Sell  (매도)
	└ 105원 → [id4]

	Result -> [id3, id1, id2, id4]

	NOTE: id1, ... 는 실제 Order 객체
*/
func (b *OrderBook) orders() []*domain.Order {
	out := make([]*domain.Order, 0, len(b.index))

	for _, side := range []map[uint64]*list.List{b.Buy, b.Sell} {
		prices := make([]uint64, 0, len(side))

		// Buy Key 저장 [100, 99]
		for p := range side {
			prices = append(prices, p)
		}

		// 정렬 [99, 100]
		slices.Sort(prices)

		// out[id3, id1, id2]
		for _, p := range prices {
			for e := side[p].Front(); e != nil; e = e.Next() {
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
