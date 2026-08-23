-- name: SaveSnapshot :exec
INSERT INTO snapshots (state, input_wal_index)
VALUES (?, ?);

-- name: LatestSnapshot :one
SELECT state, input_wal_index
FROM snapshots
ORDER BY id DESC
LIMIT 1;

-- name: PruneSnapshots :exec
DELETE FROM snapshots
WHERE id NOT IN (
  SELECT id FROM (
    SELECT id FROM snapshots ORDER BY id DESC LIMIT ?
  ) AS keep
);

-- name: OldestSnapshotWalIndex :one
SELECT input_wal_index
FROM snapshots
ORDER BY id ASC
LIMIT 1;
