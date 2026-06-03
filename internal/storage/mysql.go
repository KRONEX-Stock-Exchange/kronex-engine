package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/config"
)

const (
	maxOpenConns    = 25
	maxIdleConns    = 25
	connMaxLifetime = 5 * time.Minute
	connMaxIdleTime = 5 * time.Minute
	pingTimeout     = 5 * time.Second
)

func OpenMySQL(ctx context.Context, cfg config.MySQL) (*sql.DB, error) {
	dsnCfg := mysql.NewConfig()
	dsnCfg.User = cfg.User
	dsnCfg.Passwd = cfg.Password
	dsnCfg.Net = "tcp"
	dsnCfg.Addr = fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	dsnCfg.DBName = cfg.Database
	dsnCfg.Loc = time.Local
	dsnCfg.ParseTime = true
	dsnCfg.AllowNativePasswords = true
	dsn := dsnCfg.FormatDSN()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return db, nil
}
