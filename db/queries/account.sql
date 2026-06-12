-- name: UpdateAccountBalance :exec
UPDATE accounts
SET balance = ?, available_balance = ?
WHERE id = ?;
