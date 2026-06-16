-- name: UpdateAccountBalance :exec
UPDATE accounts
SET balance = ?, available_balance = ?
WHERE id = ?;

-- name: ActivateAccount :exec
UPDATE accounts
SET status = 'ACTIVE'
WHERE id = ?;
