package ledger

import (
	"container/list"
	"fmt"
	"slices"
	"strings"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

//////////////////////////////////////////////
// -------------- OrderBook --------------- //
//////////////////////////////////////////////

// NOTE: FIFO 원칙을 지키기 위해 반듯이 PushBack, ReadFront 해야합니다.
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
func (b *OrderBook) Front(side domain.TradingType, price uint64) (order domain.Order, ok bool) {
	queues := b.buy
	if side == domain.TRADING_SELL {
		queues = b.sell
	}

	queue, ok := queues[price]
	if !ok || queue.Len() == 0 {
		return domain.Order{}, false
	}
	return *queue.Front().Value.(*domain.Order), true
}

// 호가창에 있는 특정 주문 조회 없으면 ok=false
func (b *OrderBook) Get(orderId int64) (order domain.Order, ok bool) {
	ref, ok := b.index[orderId]
	if !ok {
		return domain.Order{}, false
	}
	return *ref.elem.Value.(*domain.Order), true
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

// 가격대별 잔량 (호가 한 단계)
type Level struct {
	Price    uint64
	Quantity uint64
}

// 한 가격대(큐)에 쌓인 모든 주문의 잔량(Quantity - FilledQuantity) 합
func levelQuantity(queue *list.List) uint64 {
	var sum uint64
	for e := queue.Front(); e != nil; e = e.Next() {
		o := e.Value.(*domain.Order)
		sum += o.Quantity - o.FilledQuantity
	}
	return sum
}

// Render 최우선호가 기준 위아래 levels 단계까지 호가창을 세로로 그린 문자열을 반환 (디버그/로그용)
// 위쪽: 매도(ASK, 높은가 → 낮은가), 가운데: 스프레드, 아래쪽: 매수(BID, 높은가 → 낮은가)
func (b *OrderBook) Render(levels int) string {
	// 매도: 최우선(최저가)부터 levels개
	asks := make([]Level, 0, levels)
	for i := 0; i < len(b.sellPrices) && i < levels; i++ {
		p := b.sellPrices[i]
		asks = append(asks, Level{Price: p, Quantity: levelQuantity(b.sell[p])})
	}
	// 매수: 최우선(최고가)부터 levels개
	bids := make([]Level, 0, levels)
	for i := 0; i < len(b.buyPrices) && i < levels; i++ {
		p := b.buyPrices[len(b.buyPrices)-1-i]
		bids = append(bids, Level{Price: p, Quantity: levelQuantity(b.buy[p])})
	}

	var sb strings.Builder
	sb.WriteString("        BID │    PRICE    │ ASK\n")
	sb.WriteString("    ────────┼─────────────┼────────\n")

	if len(asks) == 0 && len(bids) == 0 {
		sb.WriteString("            │   (empty)   │\n")
		return sb.String()
	}

	// 매도: 높은가가 위로 오도록 역순 출력
	for i := len(asks) - 1; i >= 0; i-- {
		mark := ""
		if i == 0 {
			mark = "  ← best ask"
		}
		fmt.Fprintf(&sb, "            │ %11s │ %-7d%s\n", comma(asks[i].Price), asks[i].Quantity, mark)
	}
	sb.WriteString("    ────────┼─────────────┼────────\n")
	// 매수: 높은가(최우선)부터 출력
	for i := 0; i < len(bids); i++ {
		mark := ""
		if i == 0 {
			mark = "  ← best bid"
		}
		fmt.Fprintf(&sb, "    %7d │ %11s │%s\n", bids[i].Quantity, comma(bids[i].Price), mark)
	}
	return sb.String()
}

// 천 단위 콤마 포맷 (71000 -> "71,000")
func comma(n uint64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var sb strings.Builder
	head := len(s) % 3
	if head > 0 {
		sb.WriteString(s[:head])
		sb.WriteByte(',')
	}
	for i := head; i < len(s); i += 3 {
		sb.WriteString(s[i : i+3])
		if i+3 < len(s) {
			sb.WriteByte(',')
		}
	}
	return sb.String()
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

// NOTE: OrderBook 내부에 Slice가 존재하여 복사된 값을 반환하면 안됩니다.
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
