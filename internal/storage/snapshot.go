package storage

import (
	"context"
	"database/sql"
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
