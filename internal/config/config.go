package config

import (
	"fmt"
	"os"
	"strconv"
)

type MySQL struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

type RabbitMQ struct {
	Host          string
	Port          int
	User          string
	Password      string
	PrefetchCount int
	Queue         string // 주문 수신 큐
	EventQueue    string // 이벤트 발행 큐
}

type WAL struct {
	Dir string
}

type Config struct {
	MySQL    MySQL
	RabbitMQ RabbitMQ
	WAL      WAL
}

func Load() (Config, error) {
	mysqlPort, err := envInt("MYSQL_PORT", 3306)
	if err != nil {
		return Config{}, err
	}

	rabbitPort, err := envInt("RABBITMQ_PORT", 5672)
	if err != nil {
		return Config{}, err
	}

	prefetch, err := envInt("RABBITMQ_PREFETCH_COUNT", 256)
	if err != nil {
		return Config{}, err
	}

	return Config{
		MySQL: MySQL{
			Host:     env("MYSQL_HOST", "127.0.0.1"),
			Port:     mysqlPort,
			User:     env("MYSQL_USER", "root"),
			Password: env("MYSQL_PASSWORD", "1234"),
			Database: env("MYSQL_DATABASE", "stock"),
		},
		RabbitMQ: RabbitMQ{
			Host:          env("RABBITMQ_HOST", "127.0.0.1"),
			Port:          rabbitPort,
			User:          env("RABBITMQ_USER", "guest"),
			Password:      env("RABBITMQ_PASSWORD", "guest"),
			PrefetchCount: prefetch,
			Queue:         env("RABBITMQ_QUEUE", "order_queue"),
			EventQueue:    env("RABBITMQ_EVENT_QUEUE", "event_queue"),
		},
		WAL: WAL{
			Dir: env("WAL_DIR", "./data/wal"),
		},
	}, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return n, nil
}
