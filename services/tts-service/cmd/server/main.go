package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/tuxi/dream-ai-tools/tts-service/internal/api"
	"github.com/tuxi/dream-ai-tools/tts-service/internal/task"
	"github.com/tuxi/dream-ai-tools/tts-service/internal/worker"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Worker struct {
		BaseURL    string `yaml:"base_url"`
		TimeoutMs  int    `yaml:"timeout_ms"`
		RetryTimes int    `yaml:"retry_times"`
	} `yaml:"worker"`
	Redis struct {
		Addr     string `yaml:"addr"`
		Password string `yaml:"password"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{}
	cfg.Server.Port = 8088
	cfg.Worker.BaseURL = "http://127.0.0.1:8090"
	cfg.Worker.TimeoutMs = 120000
	cfg.Worker.RetryTimes = 1

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("config file not found, using defaults", "path", path)
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	if err := yaml.NewDecoder(f).Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func buildStore(cfg *Config) task.Store {
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		slog.Warn("redis not configured, using in-memory store (data will be lost on restart)")
		return task.NewMemoryStore()
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed, falling back to in-memory store", "addr", cfg.Redis.Addr, "error", err)
		return task.NewMemoryStore()
	}

	slog.Info("using redis store", "addr", cfg.Redis.Addr)
	return task.NewRedisStore(rdb)
}

func main() {
	cfgPath := "config.yaml"
	if v := os.Getenv("CONFIG_PATH"); v != "" {
		cfgPath = v
	}

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}

	store := buildStore(cfg)
	workerClient := worker.NewClient(cfg.Worker.BaseURL, cfg.Worker.TimeoutMs)
	handler := api.NewHandler(store, workerClient, api.Config{
		WorkerRetryTimes: cfg.Worker.RetryTimes,
		WorkerTimeoutMs:  cfg.Worker.TimeoutMs,
	})

	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	v1 := r.Group("/api/v1")
	{
		v1.POST("/tts", handler.Create)
		v1.GET("/tts/result", handler.Result)
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("tts-service starting", "addr", addr,
		"worker_base_url", cfg.Worker.BaseURL,
		"retry_times", cfg.Worker.RetryTimes)
	if err := r.Run(addr); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
