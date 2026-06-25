package core

import (
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	kwal "github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

const benchStockID int32 = 1

func BenchmarkEngineHandleOrder(b *testing.B) {
	// 벤치마크 중에는 로그 출력 비용과 노이즈를 제거한다.
	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prev)

	// 매칭 없이 지정가 주문이 호가창에 쌓이는 기본 경로를 측정한다.
	b.Run("limit_no_match", func(b *testing.B) {
		e := newBenchEngine(b)
		seedBenchLedger(e, 1, 1_000_000_000_000)

		b.ReportAllocs()
		// 엔진 생성과 계좌/종목 seed 비용은 측정에서 제외한다.
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			orderID := int64(i + 1)
			if err := e.handle(benchDelivery(PatternOrderCreated, benchOrderPayload(orderID, 1, "BUY", 100, 1))); err != nil {
				b.Fatalf("handle order %d: %v", orderID, err)
			}
		}
	})

	// 이미 호가창에 있는 매도 주문 1건을 매수 주문 1건이 체결하는 경로를 측정한다.
	b.Run("match_one", func(b *testing.B) {
		e := newBenchEngine(b)
		seedBenchLedger(e, 1, 1_000_000_000_000)
		seedBenchLedger(e, 2, 1_000_000_000_000)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			makerID := int64(i*2 + 1)
			takerID := int64(i*2 + 2)
			// 체결 대상이 되는 resting maker 주문은 매 반복마다 새로 넣는다.
			addBenchRestingOrder(e, makerID, 2, domain.TRADING_SELL, 100, 1)
			if err := e.handle(benchDelivery(PatternOrderCreated, benchOrderPayload(takerID, 1, "BUY", 100, 1))); err != nil {
				b.Fatalf("handle order %d: %v", takerID, err)
			}
		}
	})

	// 매수 주문 1건이 호가창의 매도 주문 100건을 sweep하는 경로를 측정한다.
	b.Run("match_100", func(b *testing.B) {
		e := newBenchEngine(b)
		seedBenchLedger(e, 1, 1_000_000_000_000)
		seedBenchLedger(e, 2, 1_000_000_000_000)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			baseID := int64(i*101 + 1)
			// 동일 가격의 resting order 100건을 만들어 FIFO 매칭 비용을 포함한다.
			for n := 0; n < 100; n++ {
				addBenchRestingOrder(e, baseID+int64(n), 2, domain.TRADING_SELL, 100, 1)
			}
			if err := e.handle(benchDelivery(PatternOrderCreated, benchOrderPayload(baseID+100, 1, "BUY", 100, 100))); err != nil {
				b.Fatalf("handle order %d: %v", baseID+100, err)
			}
		}
	})
}

func newBenchEngine(b *testing.B) *Engine {
	b.Helper()

	// WAL append 비용은 실제 handle 경로에 포함되므로 임시 디렉터리에 실제 WAL을 연다.
	dir := b.TempDir()
	input, err := kwal.Open(filepath.Join(dir, "input"), nil)
	if err != nil {
		b.Fatalf("open input wal: %v", err)
	}
	output, err := kwal.Open(filepath.Join(dir, "output"), nil)
	if err != nil {
		b.Fatalf("open output wal: %v", err)
	}

	// Run 루프 없이 handle만 직접 호출하기 위해 필요한 필드만 구성한다.
	e := &Engine{
		input:        input,
		output:       output,
		state:        ledger.NewState(),
		dedup:        newDedup(dedupWindow),
		snapshots:    make(chan snapshotData, 1),
		outputSignal: make(chan struct{}, 1),
	}
	// benchDelivery가 사용하는 queue 이름을 data plane handler에 연결한다.
	e.routes = map[string]func(Delivery) error{"bench_data": e.handleData}
	b.Cleanup(func() {
		_ = e.Close()
	})
	return e
}

func seedBenchLedger(e *Engine, accountID int32, balance uint64) {
	// 주문 검증과 잔고 갱신이 통과할 수 있도록 계좌를 준비한다.
	acc := domain.Account{Id: accountID, Balance: balance, AvailableBalance: balance}
	e.state.Accounts.Upsert(&acc)

	// 모든 벤치 시나리오는 동일한 상장 종목을 대상으로 한다.
	stock := domain.Stock{Id: benchStockID, Price: 100, Status: domain.LISTED}
	e.state.Stocks.Upsert(&stock)

	// 매도 주문과 반복 체결이 충분히 가능하도록 넉넉한 보유 수량을 넣는다.
	holding := domain.StockBalance{
		AccountId:         accountID,
		StockId:           benchStockID,
		Quantity:          1_000_000_000,
		AvailableQuantity: 1_000_000_000,
		Average:           100,
		TotalBuyAmount:    100_000_000_000,
	}
	e.state.StockBalances.Upsert(&holding)
}

func addBenchRestingOrder(e *Engine, id int64, accountID int32, side domain.TradingType, price, quantity uint64) {
	// handle 경로 밖에서 maker 주문을 직접 호가창에 올려 체결 대상 상태를 만든다.
	e.state.OrderBooks.Get(benchStockID).Add(domain.Order{
		Id:          id,
		AccountId:   accountID,
		StockId:     benchStockID,
		Price:       price,
		Quantity:    quantity,
		OrderType:   domain.ORDER_LIMIT,
		TradingType: side,
	})
}

func benchDelivery(pattern string, payload []byte) Delivery {
	// 실제 consumer가 넘기는 envelope 형태와 동일하게 pattern/data를 감싼다.
	env, err := json.Marshal(map[string]any{
		"pattern": pattern,
		"data":    json.RawMessage(payload),
	})
	if err != nil {
		panic(err)
	}
	return Delivery{
		Queue: "bench_data",
		Message: domain.Message{
			RoutingKey: "bench_data",
			Payload:    env,
		},
		// 벤치마크는 broker 없이 handle 경로만 측정하므로 ACK/NACK은 no-op으로 둔다.
		Ack:  func() error { return nil },
		Nack: func(bool) error { return nil },
	}
}

func benchOrderPayload(id int64, accountID int32, side string, price, quantity uint64) []byte {
	// 프로덕션 메시지 포맷에 맞춰 숫자 필드도 문자열로 직렬화한다.
	payload, err := json.Marshal(map[string]string{
		"id":             stringInt(id),
		"targetId":       "0",
		"accountId":      stringInt(int64(accountID)),
		"stockId":        stringInt(int64(benchStockID)),
		"price":          stringUint(price),
		"quantity":       stringUint(quantity),
		"filledQuantity": "0",
		"orderType":      "LIMIT",
		"tradingType":    side,
	})
	if err != nil {
		panic(err)
	}
	return payload
}

func stringInt(v int64) string {
	// 벤치 payload에서 사용하는 signed 정수 문자열 변환 헬퍼.
	return strconv.FormatInt(v, 10)
}

func stringUint(v uint64) string {
	// 벤치 payload에서 사용하는 unsigned 정수 문자열 변환 헬퍼.
	return strconv.FormatUint(v, 10)
}
