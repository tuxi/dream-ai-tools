# dream-ai-tools — TTS 服务化

## 1. 项目背景与定位

本仓库是 TTS 服务化改造的**独立服务仓库**，承载两个新服务：

| 服务 | 技术栈 | 说明 |
|------|--------|------|
| `services/tts-service` | Go / Gin | TTS 任务控制层：接收请求、管理状态、调度 Worker |
| `services/tts-worker` | Python / FastAPI | TTS 执行层：调用 edge-tts CLI 生成音频文件 |

背景问题：主系统 `dream-ai-tts/ai-engine` 中的 `EdgeProvider` 通过 `exec.Command("edge-tts")` 直接调用本地 CLI，云端 Linux 环境因 Python venv / PEP 668 / PATH 差异频繁报错。本次改造将 TTS 执行能力服务化，主系统只通过 HTTP 与 Gin TTS Service 对话。

---

## 2. 与主项目的关系和对接方式

**主项目路径**：`/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine`

### 调用链

```
ai-engine（Go Workflow Engine）
    │  tts.AsyncProvider HTTP 调用
    ▼
services/tts-service（Gin，端口 8088）
    │  HTTP POST /api/v1/synthesize
    ▼
services/tts-worker（FastAPI，端口 8090）
    │  edge_tts.Communicate(...).save(...)
    ▼
本地音频文件 /data/tts/audio/{task_id}.mp3
```

### 主项目需同步改动的文件

| 文件 | 改动说明 |
|------|----------|
| `pkg/tts/providers/edge_service_provider.go` | **新增**：实现 `tts.AsyncProvider`，通过 HTTP 调 Gin TTS Service |
| `server/server.go:103` | 根据 `cfg.TTS.Edge.ServiceURL` 选择 `EdgeServiceProvider` 或旧 `EdgeProvider` |
| `config/config.go` | `TTSEdge` 结构体新增 `ServiceURL`、`SubmitTimeoutMs`、`WaitTimeoutMs`、`PollIntervalMs` |
| `config/config.example.yaml` | 新增 `tts.edge.service_url` 示例值 |

### EdgeServiceProvider 须实现的接口（`pkg/tts/provider.go`）

```go
type AsyncProvider interface {
    Provider  // Name() + Synthesize()
    SubmitSynthesize(ctx, SubmitSynthesizeRequest) (*SubmitSynthesizeResult, error)
    WaitSynthesize(ctx, WaitSynthesizeRequest) (*SynthesizeResult, error)
}
```

`SubmitSynthesize` → `POST /api/v1/tts`（返回 `task_id`）  
`WaitSynthesize` → 轮询 `GET /api/v1/tts/result?id=...` 直到 `done/failed`  
`Synthesize` → 内部串联 Submit + Wait，保持同步调用兼容

---

## 3. 目录结构规范

```
dream-ai-tools/
├── CLAUDE.md                        ← 本文件
├── CLAUDE.md.go                     ← Go 模块占位（package dream_ai_tools）
├── config/
│   ├── config.go                    ← 本仓库配置结构（待补充）
│   └── config.example.yaml          ← 含 tts.edge.service_url 示例
├── docs/
│   ├── tts-service-prd.md
│   ├── tts-service-technical-design.md
│   └── tts-service-task-breakdown.md
└── services/
    ├── tts-service/                 ← Gin TTS Service（Go）
    │   ├── cmd/server/main.go
    │   ├── internal/
    │   │   ├── api/                 ← handler：POST /api/v1/tts，GET /api/v1/tts/result
    │   │   ├── task/                ← Task 模型、内存 store、状态机
    │   │   └── worker/              ← HTTP 调 Python Worker、重试逻辑
    │   ├── config.example.yaml
    │   └── go.mod
    └── tts-worker/                  ← Python FastAPI Worker
        ├── app/
        │   └── main.py              ← POST /api/v1/synthesize，GET /healthz
        ├── requirements.txt         ← edge-tts、fastapi、uvicorn
        └── README.md
```

---

## 4. 开发任务清单

### P0 — MVP（本轮实现目标）

| ID | 任务 | 目标文件 | 验收标准 |
|----|------|----------|----------|
| P0-1 | ~~冻结 API 契约~~ | docs/ | ✅ 已完成（见 PRD / 技术设计） |
| P0-2 | Python edge-tts Worker | `services/tts-worker/` | 独立启动可生成 mp3；主系统不依赖 Python |
| P0-3 | Gin TTS Service | `services/tts-service/` | 创建任务 <100ms；状态可查询 processing/done/failed |
| P0-4 | 主系统 HTTP Edge Provider | `ai-engine/pkg/tts/providers/edge_service_provider.go` | 配置 service_url 后不再调用 edge-tts CLI |
| P0-5 | 扩展配置 | `config/config.go` + `config.example.yaml` | service_url 非空用 HTTP provider；为空保留旧 CLI |
| P0-6 | 本地联调 | — | 主系统不安装 edge-tts 仍可生成音频且 ffprobe 可读时长 |

### P1 — 生产化

| ID | 任务 | 说明 |
|----|------|------|
| P1-1 | Redis 状态与队列 | 替换内存 map；支持 Service 多实例 |
| P1-2 | OSS 上传 | Worker/Service 上传 OSS，返回稳定 URL |
| P1-3 | 结构化可观测性 | task_id / voice / chars / latency / error_code 日志 |
| P1-4 | Docker + Compose | tts-service 和 tts-worker 各自 Dockerfile；compose 示例 |

### P2 — 扩展能力

| ID | 任务 | 说明 |
|----|------|------|
| P2-1 | 多 Worker 负载均衡 | 静态列表轮询 / 最少连接 |
| P2-2 | 多 TTS 引擎接入 | engine 字段：edge \| openai \| azure \| elevenlabs \| volcengine |
| P2-3 | 任务取消与超时回收 | POST /api/v1/tts/cancel；任务租约 |

---

## 5. 主项目参考文件路径

```
/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/
├── pkg/tts/
│   ├── provider.go          ← Provider / AsyncProvider / Service 接口定义
│   ├── types.go             ← SynthesizeRequest / SynthesizeResult / SubmitSynthesizeResult 等全部类型
│   └── providers/
│       └── edge_provider.go ← 现有 CLI provider，新文件 edge_service_provider.go 参考此结构
└── server/server.go:69      ← NewServer()，TTS provider 在 :103 行初始化，需在此加分支判断
```

关键类型速查：

| 类型 | 说明 |
|------|------|
| `tts.SynthesizeRequest` | Text / Voice / Rate / Volume / Pitch / Format / Mode / TaskID … |
| `tts.SynthesizeResult` | AudioLocalPath / DurationSec / Provider / Chars / Protocol / JobID … |
| `tts.SubmitSynthesizeResult` | Provider / Protocol / Status(SubmissionStatus) / JobID / SessionID … |
| `tts.WaitSynthesizeRequest` | Provider / JobID / SessionID / WaitTimeout / PollInterval … |
| `tts.ProviderEdge` | `ProviderName = "edge"` |
| `tts.TransportProtocolAsync` | `TransportProtocol = "async"` |

---

## 6. 注意事项

### 跨容器文件访问（MVP 关键风险）

- MVP 使用本地文件存储：Worker 写 `/data/tts/audio/{task_id}.mp3`，Gin TTS Service 将路径透传给主系统。
- **主系统与 tts-service / tts-worker 必须挂载同一个 volume**，否则主系统的 `ffprobe` 读不到音频文件。
- P1-2 接入 OSS 后此问题消除，主系统改用 `url` 字段。

### 接口对齐要点

- `EdgeServiceProvider.SubmitSynthesize` 返回的 `SubmitSynthesizeResult.JobID` 对应 Gin TTS Service 的 `task_id`，`WaitSynthesize` 入参 `WaitSynthesizeRequest.JobID` 同字段。
- `SynthesizeResult.Protocol` 必须填 `tts.TransportProtocolAsync`，`Provider` 必须填 `tts.ProviderEdge`，否则主系统日志聚合会错误归类。
- Gin TTS Service 创建任务时立即将状态置为 `processing`（不是 `queued`），避免 Worker 未启动时主系统误判。

### 配置兼容策略

```yaml
# 生产环境
tts:
  edge:
    service_url: "http://tts-service:8088"   # 非空 → 使用 EdgeServiceProvider

# 开发环境（本地 macOS 可选）
tts:
  edge:
    command: "edge-tts"                       # service_url 为空时回退旧 CLI
```

`server.go` 判断优先级：`service_url` 非空 → `EdgeServiceProvider`；否则用 `EdgeProvider`（CLI）。

### 任务状态线程安全

Gin TTS Service 内存 store 须用 `sync.RWMutex` 保护，goroutine 更新状态与 HTTP 查询并发访问。

### Worker API 不对外暴露

`POST /api/v1/synthesize`（Worker）只允许 Gin TTS Service 调用，生产环境通过 Docker network 隔离，不绑定公网端口。
