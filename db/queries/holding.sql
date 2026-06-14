-- name: UpsertHolding :exec
INSERT INTO user_stocks (account_id, stock_id, quantity, available_quantity, average, total_buy_amount)
VALUES (?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  quantity = VALUES(quantity),
  available_quantity = VALUES(available_quantity),
  average = VALUES(average),
  total_buy_amount = VALUES(total_buy_amount);
