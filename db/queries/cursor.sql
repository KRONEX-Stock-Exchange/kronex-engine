-- name: LoadCursor :one
SELECT `index` FROM `cursors` WHERE `type` = ?;

-- name: SaveCursor :exec
INSERT INTO `cursors` (`type`, `index`)
VALUES (?, ?)
ON DUPLICATE KEY UPDATE `index` = VALUES(`index`);
