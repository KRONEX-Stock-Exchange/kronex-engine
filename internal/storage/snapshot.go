package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/storage/sqlc"
)

type SnapshotStore struct {
	db *sql.DB
	q  *sqlc.Queries
}

func NewSnapshotStore(db *sql.DB) *SnapshotStore {
	return &SnapshotStore{db: db, q: sqlc.New(db)}
}

// 스냅샷 저장 및 기존 스냅샷 정리
func (s *SnapshotStore) SaveSnapshotAndPrune(ctx context.Context, state []byte, inputWalIndex uint64, keep int) (floor uint64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	q := s.q.WithTx(tx)
	if err := q.SaveSnapshot(ctx, sqlc.SaveSnapshotParams{
		State:         state,
		InputWalIndex: inputWalIndex,
	}); err != nil {
		return 0, fmt.Errorf("save snapshot (walIndex=%d): %w", inputWalIndex, err)
	}
	if err := q.PruneSnapshots(ctx, int32(keep)); err != nil {
		return 0, fmt.Errorf("prune snapshots: %w", err)
	}
	floor, err = q.OldestSnapshotWalIndex(ctx)
	if err != nil {
		return 0, fmt.Errorf("oldest snapshot wal index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return floor, nil
}

// 스냅샷 정리
func (s *SnapshotStore) PruneSnapshots(ctx context.Context, keep int) (floor uint64, hasAny bool, err error) {
	if err := s.q.PruneSnapshots(ctx, int32(keep)); err != nil {
		return 0, false, fmt.Errorf("prune snapshots: %w", err)
	}
	floor, err = s.q.OldestSnapshotWalIndex(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("oldest snapshot wal index: %w", err)
	}
	return floor, true, nil
}

func (s *SnapshotStore) LatestSnapshot(ctx context.Context) (state []byte, inputWalIndex uint64, found bool, err error) {
	row, err := s.q.LatestSnapshot(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("load latest snapshot: %w", err)
	}
	return row.State, row.InputWalIndex, true, nil
}
