-- name: LoadCursor :one
SELECT `index` FROM `cursor` WHERE `type` = ?;

-- name: SaveCursor :exec
INSERT INTO `cursor` (`type`, `index`)
VALUES (?, ?)
ON DUPLICATE KEY UPDATE `index` = VALUES(`index`);
