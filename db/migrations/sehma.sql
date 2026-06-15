-- =========================================================
-- users
-- =========================================================
CREATE TABLE `users` (
  `id`         INT          NOT NULL AUTO_INCREMENT,
  `username`   VARCHAR(20)  NOT NULL,
  `password`   VARCHAR(60)  NOT NULL,
  `email`      VARCHAR(50)  NOT NULL,
  `role`       ENUM('ADMIN', 'USER') NOT NULL DEFAULT 'USER',
  `created_at` DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_users_username` (`username`),
  UNIQUE KEY `uk_users_email` (`email`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- refresh_tokens
-- =========================================================
CREATE TABLE `refresh_tokens` (
  `id`           INT          NOT NULL AUTO_INCREMENT,
  `user_id`      INT          NOT NULL,
  `hashed_token` VARCHAR(191) NOT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_refresh_tokens_user_id` (`user_id`),
  CONSTRAINT `fk_refresh_tokens_user`
    FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- accounts (일반 계좌)
-- =========================================================
CREATE TABLE `accounts` (
  `id`                INT             NOT NULL AUTO_INCREMENT,
  `user_id`           INT             NOT NULL,
  `account_number`    INT             NOT NULL,
  `balance`           BIGINT UNSIGNED NOT NULL,
  `available_balance` BIGINT UNSIGNED NOT NULL,
  `status`            ENUM('PENDING', 'ACTIVE') NOT NULL DEFAULT 'PENDING',
  `created_at`        DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_accounts_account_number` (`account_number`),
  KEY `idx_accounts_user_id` (`user_id`),
  CONSTRAINT `fk_accounts_user`
    FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- stocks (주식 정보)
-- =========================================================
CREATE TABLE `stocks` (
  `id`         INT             NOT NULL AUTO_INCREMENT,
  `name`       VARCHAR(30)     NOT NULL,
  `price`      BIGINT UNSIGNED NOT NULL,
  `status`     ENUM('PENDING', 'LISTED', 'SUSPENDED', 'DELISTED') NOT NULL DEFAULT 'PENDING',
  `created_at` DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  `updated_at` DATETIME(3)     NULL ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_stocks_name` (`name`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- stock_histories (주식 일별 히스토리)
-- =========================================================
CREATE TABLE `stock_histories` (
  `stock_id`    INT             NOT NULL,
  `date`        DATE            NOT NULL,
  `high`        BIGINT UNSIGNED NOT NULL,
  `low`         BIGINT UNSIGNED NOT NULL,
  `close`       BIGINT UNSIGNED NOT NULL,
  `open`        BIGINT UNSIGNED NULL,
  `upper_limit` BIGINT UNSIGNED NOT NULL,
  `lower_limit` BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (`stock_id`, `date`),
  CONSTRAINT `fk_stock_histories_stock`
    FOREIGN KEY (`stock_id`) REFERENCES `stocks` (`id`) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- user_stocks (주식 계좌, 보유 종목)
-- =========================================================
CREATE TABLE `user_stocks` (
  `account_id`         INT             NOT NULL,
  `stock_id`           INT             NOT NULL,
  `quantity`           BIGINT UNSIGNED NOT NULL,
  `available_quantity` BIGINT UNSIGNED NOT NULL,
  `average`            BIGINT UNSIGNED NOT NULL,
  `total_buy_amount`   BIGINT UNSIGNED NOT NULL,
  PRIMARY KEY (`account_id`, `stock_id`),
  KEY `idx_user_stocks_stock_id` (`stock_id`),
  CONSTRAINT `fk_user_stocks_account`
    FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`),
  CONSTRAINT `fk_user_stocks_stock`
    FOREIGN KEY (`stock_id`) REFERENCES `stocks` (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- orders (주문)
-- =========================================================
CREATE TABLE `orders` (
  `id`              BIGINT          NOT NULL AUTO_INCREMENT,
  `target_id`       BIGINT          NULL,
  `account_id`      INT             NOT NULL,
  `stock_id`        INT             NOT NULL,
  `price`           BIGINT UNSIGNED NOT NULL,
  `quantity`        BIGINT UNSIGNED NOT NULL,
  `filled_quantity` BIGINT UNSIGNED NOT NULL,
  `order_type`      ENUM('LIMIT', 'MARKET') NOT NULL,
  `status`          ENUM('RECEIVED', 'OPEN', 'FILLED', 'CANCELED', 'REPLACED', 'REJECTED') NOT NULL DEFAULT 'RECEIVED',
  `reject_reason`   ENUM('INSUFFICIENT_BALANCE', 'INSUFFICIENT_STOCK', 'PRICE_OUT_OF_LIMIT', 'STOCK_NOT_TRADABLE', 'ALREADY_PROCESSED') NULL,
  `trading_type`    ENUM('BUY', 'SELL', 'EDIT', 'CANCEL') NOT NULL,
  `published_at`    DATETIME(3)     NULL,
  `created_at`      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_orders_published_at` (`published_at`),
  KEY `idx_orders_account_id` (`account_id`),
  KEY `idx_orders_stock_id` (`stock_id`),
  KEY `idx_orders_target_id` (`target_id`),
  CONSTRAINT `fk_orders_account`
    FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`),
  CONSTRAINT `fk_orders_stock`
    FOREIGN KEY (`stock_id`) REFERENCES `stocks` (`id`),
  CONSTRAINT `fk_orders_target`
    FOREIGN KEY (`target_id`) REFERENCES `orders` (`id`)
    ON DELETE NO ACTION ON UPDATE NO ACTION
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- trades (체결 내역)
-- =========================================================
CREATE TABLE `trades` (
  `id`             BIGINT          NOT NULL AUTO_INCREMENT,
  `stock_id`       INT             NOT NULL,
  `price`          BIGINT UNSIGNED NOT NULL,
  `quantity`       BIGINT UNSIGNED NOT NULL,
  `maker_order_id` BIGINT          NOT NULL,
  `taker_order_id` BIGINT          NOT NULL,
  `matched_at`     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`),
  KEY `idx_trades_stock_id` (`stock_id`),
  KEY `idx_trades_maker_order_id` (`maker_order_id`),
  KEY `idx_trades_taker_order_id` (`taker_order_id`),
  CONSTRAINT `fk_trades_stock`
    FOREIGN KEY (`stock_id`) REFERENCES `stocks` (`id`),
  CONSTRAINT `fk_trades_maker_order`
    FOREIGN KEY (`maker_order_id`) REFERENCES `orders` (`id`),
  CONSTRAINT `fk_trades_taker_order`
    FOREIGN KEY (`taker_order_id`) REFERENCES `orders` (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- snapshots (엔진 상태 스냅샷)
-- =========================================================
CREATE TABLE `snapshots` (
  `id`              BIGINT          NOT NULL AUTO_INCREMENT,
  `state`           LONGBLOB        NOT NULL,
  `input_wal_index` BIGINT UNSIGNED NOT NULL,
  `created_at`      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (`id`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;

-- =========================================================
-- Cursor (커서)
-- =========================================================
CREATE TABLE `cursors` (
  `type`  ENUM('EVENT') NOT NULL,
  `index` BIGINT        NOT NULL,
  PRIMARY KEY (`type`)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_unicode_ci;
