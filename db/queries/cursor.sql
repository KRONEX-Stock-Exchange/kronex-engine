-- name: LoadMQPublishedCursor :one
SELECT `index`
FROM `cursors`
WHERE `type` = 'MQ_PUBLISHED_OUTPUT_SEQ';

-- name: LoadDBAppliedCursor :one
SELECT `index`
FROM `cursors`
WHERE `type` = 'DB_APPLIED_OUTPUT_SEQ';

-- name: SaveMQPublishedCursor :exec
INSERT INTO `cursors` (`type`, `index`)
VALUES ('MQ_PUBLISHED_OUTPUT_SEQ', ?)
ON DUPLICATE KEY UPDATE `index` = VALUES(`index`);

-- name: SaveDBAppliedCursor :exec
INSERT INTO `cursors` (`type`, `index`)
VALUES ('DB_APPLIED_OUTPUT_SEQ', ?)
ON DUPLICATE KEY UPDATE `index` = VALUES(`index`);
