-- name: UpdateOrderState :exec
UPDATE orders
SET status = ?, quantity = ?, filled_quantity = ?
WHERE id = ?;

-- name: RejectOrder :exec
UPDATE orders
SET status = 'REJECTED', reject_reason = ?
WHERE id = ?;
