-- name: CompleteTransfer :exec
UPDATE transfers
SET status = 'COMPLETED', completed_at = ?
WHERE id = ?;

-- name: RejectTransfer :exec
UPDATE transfers
SET status = 'REJECTED', reject_reason = ?, completed_at = ?
WHERE id = ?;
