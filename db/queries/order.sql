-- name: UpdateOrderStatus :exec
UPDATE orders
SET status = ?, filled_quantity = ?
WHERE id = ?;

-- name: RejectOrder :exec
UPDATE orders
SET status = 'REJECTED', reject_reason = ?
WHERE id = ?;
