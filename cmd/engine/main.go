package main

import (
	"context"
	"log"

	"github.com/joho/godotenv"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/config"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/messaging"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/storage"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx := context.Background()

	// Mysql
	db, err := storage.OpenMySQL(ctx, cfg.MySQL)
	if err != nil {
		log.Fatalf("connect mysql: %v", err)
	}
	defer db.Close()

	log.Printf("connected to MySQL at %s:%d/%s", cfg.MySQL.Host, cfg.MySQL.Port, cfg.MySQL.Database)

	// RabbitMQ
	mq, err := messaging.Open(ctx, cfg.RabbitMQ)
	if err != nil {
		log.Fatalf("connect rabbitmq: %v", err)
	}
	defer mq.Close()

	log.Printf("connected to RabbitMQ at %s:%d", cfg.RabbitMQ.Host, cfg.RabbitMQ.Port)
}
