package main

import (
	"context"
	"log"

	"github.com/joho/godotenv"

	"github.com/KRONEX-Stock-Exchange/kronex-matching-server-go/internal/config"
	"github.com/KRONEX-Stock-Exchange/kronex-matching-server-go/internal/storage"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := storage.OpenMySQL(context.Background(), cfg.MySQL)
	if err != nil {
		log.Fatalf("connect mysql: %v", err)
	}
	defer db.Close()

	log.Printf("connected to MySQL at %s:%d/%s", cfg.MySQL.Host, cfg.MySQL.Port, cfg.MySQL.Database)
}
