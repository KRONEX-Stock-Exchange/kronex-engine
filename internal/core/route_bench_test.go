package core

import (
	"flag"
	"io"
	"log"
	"math/rand"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

// -book: 켜면 match 안의 주문별 호가창 로그(ob.Render)를 stderr 로 내보내고,
// 벤치 종료 후 최종 호가창 스냅샷을 한 번 더 찍는다. 주문 수만큼 로그가 쏟아지므로
// 반복을 적게 잡는 -benchtime(예: 20x)과 함께 쓰는 걸 권장한다.
//   go test ./internal/core -run '^$' -bench BenchmarkRoute_MixedFlow -benchtime 20x -book
var benchShowBook = flag.Bool("book", false, "route 벤치에서 호가창 로그를 출력한다")

// 가라 주문을 route 에 직접 꽂아 처리 속도를 재는 벤치마크.
//
// 큐/Input WAL/검증을 거치지 않고 route 만 호출하므로 "매칭 + 원장 갱신 + Output WAL"
// 경로의 순수 처리량을 본다. 단, route 는 "지금 구조" 그대로 호출하므로 다음 두 비용이
// 포함된다(=실측치를 좌우함):
//   - match 끝에서 주문마다 만드는 ob.Render(10) 문자열 (디버그 로그용)
//   - Output WAL fsync (tidwall/wal 기본값은 쓰기마다 sync)
// 로그 출력은 io.Discard 로 돌려 stderr 스팸만 막는다(Render 연산 비용은 그대로 계측).
//
// 실행:
//   go test ./internal/core -run '^$' -bench BenchmarkRoute -benchmem
//   go test ./internal/core -run '^$' -bench BenchmarkRoute_MatchSteadyState -benchtime 2s

const (
	benchBuyer  int32  = 1001
	benchSeller int32  = 1002
	benchPrice  uint64 = 70000
	benchQty    uint64 = 10
)

// route 만 돌리는 최소 Engine. 원장에 매수/매도 계좌·종목·보유수량을 넉넉히 심어
// match 내부의 잔고/보유 Get·Upsert 경로까지 실제로 타도록 한다.
func newBenchEngine(b *testing.B) *Engine {
	b.Helper()

	out, err := wal.Open(b.TempDir(), nil)
	if err != nil {
		b.Fatalf("open output wal: %v", err)
	}
	b.Cleanup(func() { _ = out.Close() })

	e := &Engine{output: out, state: ledger.NewState()}

	const huge = uint64(1) << 62 // 벤치 동안 잔고/수량이 바닥나지 않을 만큼 큰 값
	e.state.Stocks.Upsert(&domain.Stock{Id: testStockID, Price: benchPrice, Status: domain.LISTED})
	e.state.Accounts.Upsert(&domain.Account{Id: benchBuyer, Balance: huge, AvailableBalance: huge})
	e.state.Accounts.Upsert(&domain.Account{Id: benchSeller, Balance: huge, AvailableBalance: huge})
	e.state.StockBalances.Upsert(&domain.StockBalance{
		AccountId: benchSeller, StockId: testStockID, Quantity: huge, AvailableQuantity: huge,
	})

	return e
}

// match 끝의 ob.Render 로그가 stderr 를 도배하지 않도록 출력만 버린다.
// -book 플래그가 켜져 있으면 로그를 그대로 살려 호가창 변화를 눈으로 본다.
func quietLogs(b *testing.B) {
	b.Helper()
	if *benchShowBook {
		return
	}
	prev := log.Writer()
	log.SetOutput(io.Discard)
	b.Cleanup(func() { log.SetOutput(prev) })
}

// -book 이 켜져 있으면 벤치가 끝난 뒤 쌓인 호가창의 최종 모습을 한 번 찍는다.
func dumpBook(b *testing.B, e *Engine) {
	b.Helper()
	if !*benchShowBook {
		return
	}
	ob := e.state.OrderBooks.Get(testStockID)
	log.Printf("[%s] 최종 호가창 stock=%d (±10단계)\n%s", b.Name(), testStockID, ob.Render(10))
}

func benchOrder(id int64, side domain.TradingType, acc int32) domain.Order {
	o := limit(id, side, benchPrice, benchQty)
	o.AccountId = acc
	return o
}

// [시나리오] 매도→매수 교차를 번갈아 던져 정상 체결이 계속 일어나는 정상 상태.
// 매 짝수 주문(매도)은 빈 호가창에 잠깐 등록되고, 바로 뒤 홀수 주문(매수)이 전량 체결한다.
// 호가창 깊이는 0~1 로 유지되므로 "주문 1건당 평균 비용"을 깔끔하게 본다(절반이 체결 발생).
func BenchmarkRoute_MatchSteadyState(b *testing.B) {
	quietLogs(b)
	e := newBenchEngine(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var o domain.Order
		if i%2 == 0 {
			o = benchOrder(int64(i+1), domain.TRADING_SELL, benchSeller)
		} else {
			o = benchOrder(int64(i+1), domain.TRADING_BUY, benchBuyer)
		}
		if err := e.route(o); err != nil {
			b.Fatalf("route: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "orders/s")
	dumpBook(b, e)
}

// [시나리오] 실전형 혼합 흐름. 매수/매도 50:50, 가격을 mid ±수 틱으로 흩뿌려
// 교차해 체결되는 주문과 호가창에 쌓이는 주문이 섞이고, 호가에 깊이가 생겨
// 부분체결·다단계 쓸기까지 자연스럽게 나온다. 약 10%는 시장가.
//
// RNG 비용을 측정에서 빼려고 주문을 타이머 시작 전에 미리 만들어 두고 replay 한다.
// 시드 고정이라 매 실행 같은 주문열(재현 가능). 매수=benchBuyer, 매도=benchSeller 로
// 고정해 자전거래는 발생하지 않는다.
func BenchmarkRoute_MixedFlow(b *testing.B) {
	quietLogs(b)
	e := newBenchEngine(b)

	const (
		tick   uint64 = 100
		spread        = 3 // mid 기준 ±3 틱 범위에 가격 분포
		maxQty uint64 = 20
	)
	rng := rand.New(rand.NewSource(1))

	// 미리 b.N 개 주문 생성 (측정 구간 밖)
	orders := make([]domain.Order, b.N)
	for i := range orders {
		buy := rng.Intn(2) == 0
		off := uint64(rng.Intn(spread + 1)) // 0..spread
		qty := uint64(rng.Intn(int(maxQty))) + 1
		market := rng.Intn(10) == 0 // 약 10% 시장가

		var o domain.Order
		if buy {
			// 매수: mid 이상이면 대기 매도와 교차, 미만이면 호가창에 등록
			o = benchOrder(int64(i+1), domain.TRADING_BUY, benchBuyer)
			o.Price = benchPrice + off*tick
		} else {
			o = benchOrder(int64(i+1), domain.TRADING_SELL, benchSeller)
			o.Price = benchPrice - off*tick
		}
		o.Quantity = qty
		if market {
			o.OrderType = domain.ORDER_MARKET
		}
		orders[i] = o
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.route(orders[i]); err != nil {
			b.Fatalf("route: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "orders/s")
	dumpBook(b, e)
}

// [시나리오] 유동성이 없어 전부 호가창에 쌓이기만 하는 미체결 경로.
// 같은 가격대에 매도만 계속 던져 한 호가 레벨에 FIFO 로 누적시킨다(가격 레벨 정렬비용 배제).
// 체결 없이 "주문 등록 + Output WAL" 비용만 분리해서 본다.
func BenchmarkRoute_RestOnly(b *testing.B) {
	quietLogs(b)
	e := newBenchEngine(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o := benchOrder(int64(i+1), domain.TRADING_SELL, benchSeller)
		if err := e.route(o); err != nil {
			b.Fatalf("route: %v", err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "orders/s")
	dumpBook(b, e)
}
