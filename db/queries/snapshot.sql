-- name: SaveSnapshot :exec
INSERT INTO snapshots (state, input_wal_index)
VALUES (?, ?);

-- name: LatestSnapshot :one
SELECT state, input_wal_index
FROM snapshots
ORDER BY id DESC
LIMIT 1;
