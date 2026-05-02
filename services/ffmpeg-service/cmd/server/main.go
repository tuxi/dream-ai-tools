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
	"github.com/tuxi/dream-ai-tools/ffmpeg-service/internal/api"
	"github.com/tuxi/dream-ai-tools/ffmpeg-service/internal/executor"
	"github.com/tuxi/dream-ai-tools/ffmpeg-service/internal/job"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Executor struct {
		WorkDir       string `yaml:"work_dir"`
		FFmpegPath    string `yaml:"ffmpeg_path"`
		FFprobePath   string `yaml:"ffprobe_path"`
		MaxConcurrent int    `yaml:"max_concurrent"`
		RetryTimes    int    `yaml:"retry_times"`
		TimeoutMs     int    `yaml:"timeout_ms"`
	} `yaml:"executor"`
	Redis struct {
		Addr     string `yaml:"addr"`
		Password string `yaml:"password"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{}
	cfg.Server.Port = 8089
	cfg.Executor.WorkDir = "/data/media/output"
	cfg.Executor.FFmpegPath = "ffmpeg"
	cfg.Executor.FFprobePath = "ffprobe"
	cfg.Executor.MaxConcurrent = 4
	cfg.Executor.RetryTimes = 1
	cfg.Executor.TimeoutMs = 300000

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

func buildStore(cfg *Config) job.Store {
	if strings.TrimSpace(cfg.Redis.Addr) == "" {
		slog.Warn("redis not configured, using in-memory store (data will be lost on restart)")
		return job.NewMemoryStore()
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
		return job.NewMemoryStore()
	}

	slog.Info("using redis store", "addr", cfg.Redis.Addr)
	return job.NewRedisStore(rdb)
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

	if err := os.MkdirAll(cfg.Executor.WorkDir, 0o755); err != nil {
		slog.Error("create work_dir failed", "work_dir", cfg.Executor.WorkDir, "error", err)
		os.Exit(1)
	}

	store := buildStore(cfg)
	handler := api.NewHandler(store, api.Config{
		RetryTimes: cfg.Executor.RetryTimes,
		TimeoutMs:  cfg.Executor.TimeoutMs,
		ExecConfig: executor.Config{
			WorkDir:     cfg.Executor.WorkDir,
			FFmpegPath:  cfg.Executor.FFmpegPath,
			FFprobePath: cfg.Executor.FFprobePath,
		},
	})

	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	v1 := r.Group("/api/v1/ffmpeg")
	{
		v1.POST("/jobs", handler.Submit)
		v1.GET("/jobs/result", handler.Result)
		v1.POST("/probe", handler.ProbeHandler)
	}

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	slog.Info("ffmpeg-service starting", "addr", addr,
		"work_dir", cfg.Executor.WorkDir,
		"ffmpeg_path", cfg.Executor.FFmpegPath,
		"retry_times", cfg.Executor.RetryTimes,
		"timeout_ms", cfg.Executor.TimeoutMs)
	if err := r.Run(addr); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
