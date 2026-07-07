-- name: SaveTrade :execlastid
INSERT INTO trades (stock_id, price, quantity, maker_order_id, taker_order_id, matched_at)
VALUES (?, ?, ?, ?, ?, ?);
