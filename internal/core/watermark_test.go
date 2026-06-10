package core

import (
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/domain"
)

func outputCount(t *testing.T, e *Engine) uint64 {
	t.Helper()
	last, err := e.output.LastIndex()
	if err != nil {
		t.Fatalf("output last index: %v", err)
	}
	return last
}

// [시나리오] seq=1,2 입력의 출력을 기록 → 워터마크 적재 후 seq=1,2 재생 / seq=3 신규
// [기대]    워터마크(2) 이하 재생은 스킵, 초과(3)는 기록 — 복구 중복 방지
func TestOutputWatermark_SkipsReplayedAndWritesNew(t *testing.T) {
	e := newMatchEngine(t)

	// 라이브 기록: seq=1, seq=2
	e.inputSeq = 1
	if err := e.appendOutput(outEvent{PatternTradeExecuted, domain.Trade{TakerOrderId: 1, Quantity: 10}}); err != nil {
		t.Fatalf("append seq1: %v", err)
	}
	e.inputSeq = 2
	if err := e.appendOutput(outEvent{PatternTradeExecuted, domain.Trade{TakerOrderId: 2, Quantity: 20}}); err != nil {
		t.Fatalf("append seq2: %v", err)
	}
	if got := outputCount(t, e); got != 2 {
		t.Fatalf("output count = %d, want 2", got)
	}

	// 복구 워터마크 적재 → 마지막 출력의 Seq(2)
	if err := e.loadOutputWatermark(); err != nil {
		t.Fatalf("load watermark: %v", err)
	}
	if e.outputAppliedSeq != 2 {
		t.Fatalf("watermark = %d, want 2", e.outputAppliedSeq)
	}

	// 재생: seq=1, seq=2 는 워터마크 이하 → 스킵 (출력 개수 불변)
	e.inputSeq = 1
	if err := e.appendOutput(outEvent{PatternTradeExecuted, domain.Trade{TakerOrderId: 1, Quantity: 10}}); err != nil {
		t.Fatalf("replay seq1: %v", err)
	}
	e.inputSeq = 2
	if err := e.appendOutput(outEvent{PatternTradeExecuted, domain.Trade{TakerOrderId: 2, Quantity: 20}}); err != nil {
		t.Fatalf("replay seq2: %v", err)
	}
	if got := outputCount(t, e); got != 2 {
		t.Fatalf("replay <= watermark should skip, output count = %d, want 2", got)
	}

	// seq=3 은 워터마크 초과 → 기록
	e.inputSeq = 3
	if err := e.appendOutput(outEvent{PatternTradeExecuted, domain.Trade{TakerOrderId: 3, Quantity: 30}}); err != nil {
		t.Fatalf("append seq3: %v", err)
	}
	if got := outputCount(t, e); got != 3 {
		t.Fatalf("new seq should write, output count = %d, want 3", got)
	}
}

// [시나리오] 출력 WAL 이 비어 있을 때 워터마크 적재
// [기대]    워터마크 0, 이후 기록은 스킵되지 않음
func TestOutputWatermark_EmptyOutputNoSkip(t *testing.T) {
	e := newMatchEngine(t)
	if err := e.loadOutputWatermark(); err != nil {
		t.Fatalf("load watermark: %v", err)
	}
	if e.outputAppliedSeq != 0 {
		t.Fatalf("watermark = %d, want 0 (empty output)", e.outputAppliedSeq)
	}

	e.inputSeq = 1
	if err := e.appendOutput(outEvent{PatternTradeExecuted, domain.Trade{TakerOrderId: 1, Quantity: 10}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := outputCount(t, e); got != 1 {
		t.Fatalf("output count = %d, want 1 (no skip when watermark 0)", got)
	}
}
