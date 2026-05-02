# 主项目 server.go 修改说明

文件：`ai-engine/server/server.go`

## 1. config.go 新增字段

在 `TTSEdge` 结构体中增加：

```go
type TTSEdge struct {
    Command         string `mapstructure:"command"`          // 旧 CLI，保留兼容
    ServiceURL      string `mapstructure:"service_url"`      // 新增：HTTP provider URL
    SubmitTimeoutMs int    `mapstructure:"submit_timeout_ms"` // 默认 1000
    WaitTimeoutMs   int    `mapstructure:"wait_timeout_ms"`   // 默认 90000
    PollIntervalMs  int    `mapstructure:"poll_interval_ms"`  // 默认 1000
}
```

## 2. config.example.yaml 新增示例

```yaml
tts:
  enabled: true
  edge:
    service_url: "http://tts-service:8088"   # 生产环境使用
    submit_timeout_ms: 1000
    wait_timeout_ms: 90000
    poll_interval_ms: 1000
    command: ""                               # 留空；本地开发可填 "edge-tts"
```

## 3. server.go:103 修改 EdgeProvider 初始化

将：

```go
edgeProvider, err := providers.NewEdgeProvider(os.TempDir(), cfg.TTS.Edge.Command, "ffprobe")
if err != nil {
    panic(err)
}
```

改为：

```go
var edgeProvider aitts.AsyncProvider
if strings.TrimSpace(cfg.TTS.Edge.ServiceURL) != "" {
    edgeProvider = providers.NewEdgeServiceProvider(providers.EdgeServiceConfig{
        ServiceURL:      cfg.TTS.Edge.ServiceURL,
        SubmitTimeoutMs: cfg.TTS.Edge.SubmitTimeoutMs,
        WaitTimeoutMs:   cfg.TTS.Edge.WaitTimeoutMs,
        PollIntervalMs:  cfg.TTS.Edge.PollIntervalMs,
    })
} else {
    var err error
    edgeProvider, err = providers.NewEdgeProvider(os.TempDir(), cfg.TTS.Edge.Command, "ffprobe")
    if err != nil {
        panic(err)
    }
}
```

> `NewEdgeProvider` 返回 `*EdgeProvider`，它只实现了 `Provider`（无 Submit/Wait）。
> 如果 `tts.NewDefaultService` 需要 `AsyncProvider`，可以用以下 wrapper：
>
> ```go
> // 将同步 Provider 包成 AsyncProvider（如需要）
> edgeProvider = aitts.WrapSyncProvider(localEdge)
> ```
>
> 或者直接让 `NewDefaultService` 接受 `Provider` 而非 `AsyncProvider`，
> 视主项目 service.go 实际签名而定。
