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

func TestTruncateFrontKeepsIndexOnwardAndDropsBefore(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "wal"), nil)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	for i := 1; i <= 5; i++ {
		if _, err := w.Append([]byte(strconv.Itoa(i))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	if err := w.TruncateFront(3); err != nil {
		t.Fatalf("truncateFront(3): %v", err)
	}

	first, err := w.FirstIndex()
	if err != nil {
		t.Fatalf("first index: %v", err)
	}
	if first != 3 {
		t.Fatalf("first index = %d, want 3", first)
	}

	if _, err := w.Read(2); err == nil {
		t.Error("read(2) should fail, record before truncateFront index must be gone")
	}
	data, err := w.Read(3)
	if err != nil {
		t.Fatalf("read(3) should still succeed: %v", err)
	}
	if string(data) != "3" {
		t.Errorf("read(3) = %q, want %q (truncateFront index itself must be kept)", data, "3")
	}

	// 같은 index로 재호출은 no-op (에러 없음) — 재시도 안전성
	if err := w.TruncateFront(3); err != nil {
		t.Errorf("re-truncateFront(3) should be a no-op, got: %v", err)
	}
}

func TestTruncateFrontAtLastIndexPlusOneReturnsErrOutOfRange(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "wal"), nil)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := w.Append([]byte("only")); err != nil {
		t.Fatalf("append: %v", err)
	}

	last, err := w.LastIndex()
	if err != nil {
		t.Fatalf("last index: %v", err)
	}

	// 유휴 구간(새 입력이 없어 truncateFront 대상 index가 곧 lastIndex)에서 발생하는
	// 정상 케이스: AllowEmpty 없이는 로그를 통째로 비울 수 없어 ErrOutOfRange.
	// 호출자(engine.go)는 이를 안전한 no-op으로 취급해야 한다.
	if err := w.TruncateFront(last + 1); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("truncateFront(lastIndex+1) = %v, want ErrOutOfRange", err)
	}
}
