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

type Config struct {
	MySQL MySQL
}

func Load() (Config, error) {
	port, err := envInt("MYSQL_PORT", 3306)
	if err != nil {
		return Config{}, err
	}

	return Config{
		MySQL: MySQL{
			Host:     env("MYSQL_HOST", "127.0.0.1"),
			Port:     port,
			User:     env("MYSQL_USER", "root"),
			Password: env("MYSQL_PASSWORD", "1234"),
			Database: env("MYSQL_DATABASE", "stock"),
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
