-- name: UpdateStockStatus :exec
UPDATE stocks
SET status = ?
WHERE id = ?;

-- name: UpdateStockPrice :exec
UPDATE stocks
SET price = ?
WHERE id = ?;
