-- name: CompleteAdminRequest :exec
UPDATE admin_requests
SET status = 'COMPLETED', completed_at = ?
WHERE id = ?;

-- name: RejectAdminRequest :exec
UPDATE admin_requests
SET status = 'REJECTED', reject_reason = ?, completed_at = ?
WHERE id = ?;
