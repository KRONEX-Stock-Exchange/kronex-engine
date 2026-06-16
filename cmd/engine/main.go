package main

import (
	"context"
	"errors"
	"log"
	"os/signal"
	"sync"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/config"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/core"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/messaging"
	"github.com/KRONEX-Stock-Exchange/kronex-engine/internal/publisher"
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

	// 엔진
	snapStore := storage.NewSnapshotStore(db)
	engine, err := core.NewEngine(mq, snapStore, map[string]core.Plane{
		cfg.RabbitMQ.DataQueue:  core.PlaneData,
		cfg.RabbitMQ.AdminQueue: core.PlaneAdmin,
	})
	if err != nil {
		log.Fatalf("create engine: %v", err)
	}
	defer engine.Close()

	// 퍼블리셔
	eventStore := storage.NewEventStore(db)
	pub := publisher.New(engine.Output(), engine.OutputSignal(), eventStore, mq, cfg.RabbitMQ.EventQueue)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("publisher publishing to queue %q", cfg.RabbitMQ.EventQueue)
		if err := pub.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("publisher stopped: %v", err)
		}
	}()

	log.Printf("engine consuming queues %q, %q", cfg.RabbitMQ.DataQueue, cfg.RabbitMQ.AdminQueue)
	if err := engine.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("engine stopped: %v", err)
	}

	cancel()  // 엔진 종료 → 퍼블리셔도 종료
	wg.Wait() // 퍼블리셔 완전 종료 후 defer 로 WAL/연결 정리

	log.Println("engine shut down")
}
