package ledger

import (
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/ledger/ledgerpb"
)

const snapshotVersion = 1

type State struct {
}

func NewState() *State {
	return &State{}
}

// 전체 상태 직렬화
func (s *State) Serialize() ([]byte, error) {
	snap := &ledgerpb.LedgerSnapshot{
		Version: snapshotVersion,
	}

	data, err := proto.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("marshal ledger snapshot: %w", err)
	}
	return data, nil
}

// 역직렬화
func (s *State) Restore(data []byte) error {
	var snap ledgerpb.LedgerSnapshot
	if err := proto.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("unmarshal ledger snapshot: %w", err)
	}

	if snap.Version != snapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d (want %d)", snap.Version, snapshotVersion)
	}
	return nil
}
