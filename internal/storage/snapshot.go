package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/storage/sqlc"
)

type SnapshotStore struct {
	q *sqlc.Queries
}

func NewSnapshotStore(db *sql.DB) *SnapshotStore {
	return &SnapshotStore{q: sqlc.New(db)}
}

func (s *SnapshotStore) SaveSnapshot(ctx context.Context, state []byte, inputWalIndex uint64) error {
	if err := s.q.SaveSnapshot(ctx, sqlc.SaveSnapshotParams{
		State:         state,
		InputWalIndex: inputWalIndex,
	}); err != nil {
		return fmt.Errorf("save snapshot (walIndex=%d): %w", inputWalIndex, err)
	}
	return nil
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
