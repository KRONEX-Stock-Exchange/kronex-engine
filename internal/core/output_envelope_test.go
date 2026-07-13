package core

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/wal"
)

func TestAppendOutputRecordsInputAndOutputSequences(t *testing.T) {
	output, err := wal.Open(filepath.Join(t.TempDir(), "output"), nil)
	if err != nil {
		t.Fatalf("open output WAL: %v", err)
	}
	t.Cleanup(func() { _ = output.Close() })

	if _, err := output.Append([]byte(`{"inputSeq":1,"events":[]}`)); err != nil {
		t.Fatalf("seed output WAL: %v", err)
	}

	e := &Engine{
		output:       output,
		inputSeq:     42,
		outputSignal: make(chan struct{}, 1),
	}
	if err := e.appendOutput(outEvent{pattern: "test.event", data: struct{}{}}); err != nil {
		t.Fatalf("append output: %v", err)
	}

	raw, err := output.Read(2)
	if err != nil {
		t.Fatalf("read output WAL: %v", err)
	}
	var env OutputEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal output envelope: %v", err)
	}
	if env.InputSeq != 42 {
		t.Errorf("input sequence = %d, want 42", env.InputSeq)
	}
	if env.OutputSeq != 2 {
		t.Errorf("output sequence = %d, want 2", env.OutputSeq)
	}
	if env.CreatedAt.IsZero() {
		t.Error("createdAt is not recorded")
	}
}
