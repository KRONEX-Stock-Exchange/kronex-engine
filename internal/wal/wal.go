package wal

import (
	"errors"
	"fmt"
	"sync"

	twal "github.com/tidwall/wal"
)

var ErrNotFound = twal.ErrNotFound

type Options = twal.Options

type WAL struct {
	mu  sync.Mutex
	log *twal.Log
}

func Open(path string, opts *Options) (*WAL, error) {
	log, err := twal.Open(path, opts)
	if err != nil {
		return nil, fmt.Errorf("open wal at %q: %w", path, err)
	}
	return &WAL{log: log}, nil
}

func (w *WAL) Append(data []byte) (uint64, error) {
	return w.AppendWithIndex(func(uint64) ([]byte, error) {
		return data, nil
	})
}

// AppendWithIndex는 새 레코드의 인덱스를 builder에 전달해 만든 데이터를 기록한다.
// 인덱스 산정과 데이터 기록은 같은 락 안에서 수행된다.
func (w *WAL) AppendWithIndex(builder func(index uint64) ([]byte, error)) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	last, err := w.log.LastIndex()
	if err != nil {
		return 0, fmt.Errorf("read last index: %w", err)
	}

	index := last + 1
	data, err := builder(index)
	if err != nil {
		return 0, fmt.Errorf("build wal index %d: %w", index, err)
	}
	if err := w.log.Write(index, data); err != nil {
		return 0, fmt.Errorf("write wal index %d: %w", index, err)
	}
	return index, nil
}

// WAL 작성 배치처리, 첫 레코드의 인덱스를 반환 및 datas 가 빌 경우 아무것도 쓰지 않음
func (w *WAL) AppendBatch(datas [][]byte) (firstIndex uint64, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(datas) == 0 {
		return 0, nil
	}

	last, err := w.log.LastIndex()
	if err != nil {
		return 0, fmt.Errorf("read last index: %w", err)
	}

	var batch twal.Batch
	for i, data := range datas {
		batch.Write(last+1+uint64(i), data)
	}
	if err := w.log.WriteBatch(&batch); err != nil {
		return 0, fmt.Errorf("write wal batch (%d entries): %w", len(datas), err)
	}
	return last + 1, nil
}

func (w *WAL) Read(index uint64) ([]byte, error) {
	data, err := w.log.Read(index)
	if err != nil {
		return nil, fmt.Errorf("read wal index %d: %w", index, err)
	}
	return data, nil
}

func (w *WAL) FirstIndex() (uint64, error) {
	idx, err := w.log.FirstIndex()
	if err != nil {
		return 0, fmt.Errorf("read first index: %w", err)
	}
	return idx, nil
}

func (w *WAL) LastIndex() (uint64, error) {
	idx, err := w.log.LastIndex()
	if err != nil {
		return 0, fmt.Errorf("read last index: %w", err)
	}
	return idx, nil
}

func (w *WAL) Checkpoint(index uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.log.TruncateFront(index); err != nil {
		return fmt.Errorf("truncate wal front to %d: %w", index, err)
	}
	return nil
}

func (w *WAL) Sync() error {
	if err := w.log.Sync(); err != nil {
		return fmt.Errorf("sync wal: %w", err)
	}
	return nil
}

func (w *WAL) Close() error {
	if w.log == nil {
		return nil
	}
	if err := w.log.Close(); err != nil && !errors.Is(err, twal.ErrClosed) {
		return fmt.Errorf("close wal: %w", err)
	}
	return nil
}
