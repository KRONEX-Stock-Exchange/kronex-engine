-- name: UpdateOrderStatus :exec
UPDATE orders
SET status = ?, filled_quantity = ?
WHERE id = ?;
