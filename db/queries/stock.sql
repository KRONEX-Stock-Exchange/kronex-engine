-- name: UpdateStockStatus :exec
UPDATE stocks
SET status = ?
WHERE id = ?;
