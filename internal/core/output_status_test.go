package core

import (
	"encoding/json"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

// Output WAL 의 모든 봉투를 순서대로 읽는다.
func outputEnvelopesOf(t *testing.T, e *Engine) []outputEnvelope {
	t.Helper()
	last, err := e.output.LastIndex()
	if err != nil {
		t.Fatalf("output last index: %v", err)
	}
	var out []outputEnvelope
	for i := uint64(1); i <= last; i++ {
		data, err := e.output.Read(i)
		if err != nil {
			t.Fatalf("read output %d: %v", i, err)
		}
		var env outputEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("unmarshal output %d: %v", i, err)
		}
		out = append(out, env)
	}
	return out
}

func orderEventOf(t *testing.T, raw json.RawMessage) domain.OrderEvent {
	t.Helper()
	var ev domain.OrderEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal order event: %v", err)
	}
	return ev
}

// [시나리오] 빈 호가창에 지정가 매수 → 체결 없이 등록만
// [기대]    체결 0건이라도 order.open 이벤트가 출력됨 (잔량 50)
func TestOutput_RestedOrderEmitsOpen(t *testing.T) {
	e := newMatchEngine(t)
	if err := e.match(limit(1, domain.TRADING_BUY, 70_000, 50)); err != nil {
		t.Fatalf("match: %v", err)
	}

	envs := outputEnvelopesOf(t, e)
	if len(envs) != 1 {
		t.Fatalf("output = %d, want 1 (order.open 만)", len(envs))
	}
	if envs[0].Pattern != PatternOrderOpen {
		t.Fatalf("pattern = %q, want %q", envs[0].Pattern, PatternOrderOpen)
	}
	ev := orderEventOf(t, envs[0].Data)
	if ev.OrderId != 1 || ev.Quantity != 50 || ev.FilledQuantity != 0 {
		t.Fatalf("order event = %+v, want id=1 qty=50 filled=0", ev)
	}
}

// [시나리오] 대기 매도 50 을 매수 50 이 전량 체결
// [기대]    체결(trade.executed) + 결과 상태(order.filled) 가 한 배치로 출력
func TestOutput_FullFillEmitsFilledWithTrade(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e, limit(1, domain.TRADING_SELL, 70_000, 50))
	if err := e.match(limit(2, domain.TRADING_BUY, 70_000, 50)); err != nil {
		t.Fatalf("match: %v", err)
	}

	envs := outputEnvelopesOf(t, e)
	if len(envs) != 2 {
		t.Fatalf("output = %d, want 2 (trade + order.filled)", len(envs))
	}
	if envs[0].Pattern != PatternTradeExecuted {
		t.Fatalf("output[0] pattern = %q, want %q", envs[0].Pattern, PatternTradeExecuted)
	}
	if envs[1].Pattern != PatternOrderFilled {
		t.Fatalf("output[1] pattern = %q, want %q", envs[1].Pattern, PatternOrderFilled)
	}
	if ev := orderEventOf(t, envs[1].Data); ev.OrderId != 2 || ev.FilledQuantity != 50 {
		t.Fatalf("order event = %+v, want id=2 filled=50", ev)
	}
}

// [시나리오] 시장가 매수 100 인데 유동성 30 만 → 30 체결, 70 취소
// [기대]    체결 + order.canceled (체결량 30 기록)
func TestOutput_MarketRemainderEmitsCanceled(t *testing.T) {
	e := newMatchEngine(t)
	seed(t, e, limit(1, domain.TRADING_SELL, 70_000, 30))
	if err := e.match(market(2, domain.TRADING_BUY, 100_000, 100)); err != nil {
		t.Fatalf("match: %v", err)
	}

	envs := outputEnvelopesOf(t, e)
	if len(envs) != 2 {
		t.Fatalf("output = %d, want 2 (trade + order.canceled)", len(envs))
	}
	last := envs[len(envs)-1]
	if last.Pattern != PatternOrderCanceled {
		t.Fatalf("마지막 pattern = %q, want %q", last.Pattern, PatternOrderCanceled)
	}
	if ev := orderEventOf(t, last.Data); ev.Quantity != 100 || ev.FilledQuantity != 30 {
		t.Fatalf("order event = %+v, want qty=100 filled=30", ev)
	}
}
