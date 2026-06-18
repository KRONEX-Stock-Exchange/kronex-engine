-- name: SaveTrade :exec
INSERT INTO trades (id, stock_id, price, quantity, maker_order_id, taker_order_id, matched_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE id = id;
