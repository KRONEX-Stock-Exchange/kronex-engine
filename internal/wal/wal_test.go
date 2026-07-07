package wal

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAppendWithIndexRecordsAllocatedIndex(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "wal"), nil)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := w.Append([]byte("first")); err != nil {
		t.Fatalf("append first record: %v", err)
	}

	index, err := w.AppendWithIndex(func(index uint64) ([]byte, error) {
		return []byte(strconv.FormatUint(index, 10)), nil
	})
	if err != nil {
		t.Fatalf("append with index: %v", err)
	}
	if index != 2 {
		t.Fatalf("index = %d, want 2", index)
	}

	data, err := w.Read(index)
	if err != nil {
		t.Fatalf("read appended record: %v", err)
	}
	if got := string(data); got != "2" {
		t.Errorf("record = %q, want %q", got, "2")
	}
}

func TestAppendWithIndexDoesNotWriteWhenBuilderFails(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "wal"), nil)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	wantErr := errors.New("build failed")
	if _, err := w.AppendWithIndex(func(uint64) ([]byte, error) {
		return nil, wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("append error = %v, want %v", err, wantErr)
	}

	last, err := w.LastIndex()
	if err != nil {
		t.Fatalf("last index: %v", err)
	}
	if last != 0 {
		t.Errorf("last index = %d, want 0", last)
	}
}
