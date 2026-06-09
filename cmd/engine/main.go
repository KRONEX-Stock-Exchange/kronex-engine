package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/config"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/messaging"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/storage"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	// 엔진 실행
	engine, err := core.NewEngine(mq, cfg.RabbitMQ.Queue)
	if err != nil {
		log.Fatalf("create engine: %v", err)
	}
	defer engine.Close()

	log.Printf("engine consuming queue %q", cfg.RabbitMQ.Queue)
	if err := engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("engine stopped: %v", err)
	}

	log.Println("engine shut down")
}
