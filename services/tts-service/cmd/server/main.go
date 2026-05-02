package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
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
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{}
	cfg.Server.Port = 8088
	cfg.Worker.BaseURL = "http://localhost:8090"
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

	store := task.NewStore()
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
