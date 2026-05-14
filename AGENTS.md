# dream-ai-tools — TTS 与 FFmpeg 服务化

## 1. 项目背景与定位

本仓库承载将 AI Engine 中媒体处理能力服务化的独立服务，目前包含三个服务：

| 服务 | 技术栈 | 端口 | 说明 |
|------|--------|------|------|
| `services/tts-service` | Go / Gin | 8088 | TTS 任务控制层：接收请求、管理状态、调度 Worker |
| `services/tts-worker` | Python / FastAPI | 8090 | TTS 执行层：调用 edge-tts CLI 生成音频文件 |
| `services/ffmpeg-service` | Go / Gin | 8089 | FFmpeg 媒体处理层：10 种操作 + probe，替代主系统 exec.Command |

**TTS 背景**：主系统 `EdgeProvider` 通过 `exec.Command("edge-tts")` 调用本地 CLI，云端 Linux 因 Python venv / PEP 668 / PATH 差异频繁报错。

**FFmpeg 背景**：主系统 11 类工具直接调用 `exec.Command("ffmpeg"/"ffprobe")`，导致镜像体积增大（+100 MB）、升级困难、工作流同步阻塞。

---

## 2. TTS Service — 与主项目对接

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

### TTS Service 接口清单

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/tts` | 提交合成任务，返回 `task_id` |
| GET  | `/api/v1/tts/result?id=` | 查询任务状态/结果 |
| GET  | `/healthz` | 健康检查 |

### 主项目需同步改动的文件（TTS）

| 文件 | 改动说明 |
|------|----------|
| `pkg/tts/providers/edge_service_provider.go` | **新增**（见 `docs/output/edge_service_provider.go`） |
| `server/server.go:103` | 根据 `cfg.TTS.Edge.ServiceURL` 选择 Provider |
| `config/config.go` | `TTSEdge` 新增 `ServiceURL` / `SubmitTimeoutMs` / `WaitTimeoutMs` / `PollIntervalMs` |
| `config/config.example.yaml` | 新增 `tts.edge.service_url` 示例值 |

### EdgeServiceProvider 须实现的接口（`pkg/tts/provider.go`）

```go
type AsyncProvider interface {
    Provider  // Name() + Synthesize()
    SubmitSynthesize(ctx, SubmitSynthesizeRequest) (*SubmitSynthesizeResult, error)
    WaitSynthesize(ctx, WaitSynthesizeRequest) (*SynthesizeResult, error)
}
```

### TTS Service 启动方式

```bash
# 本地开发
cd services/tts-service
cp config.example.yaml config.yaml   # 按需修改 worker.base_url / redis.addr
go run ./cmd/server

# Docker（需配套启动 tts-worker）
docker compose up tts-service tts-worker
```

---

## 3. FFmpeg Service — 与主项目对接

### 调用链

```
ai-engine（Go Workflow Engine）
    │  FFmpegServiceProvider HTTP 调用
    ▼
services/ffmpeg-service（Gin，端口 8089）
    │  exec.Command("ffmpeg") / exec.Command("ffprobe")
    ▼
ffmpeg / ffprobe 二进制（只在此容器内安装）
    │
    ▼
/data/media/（共享 Volume）
```

### FFmpeg Service 接口清单

| 方法 | 路径 | 模式 | 说明 |
|------|------|------|------|
| POST | `/api/v1/ffmpeg/jobs` | 异步 | 提交任务，返回 `job_id` + `status=processing` |
| GET  | `/api/v1/ffmpeg/jobs/result?id=` | 轮询 | 查询任务状态 / `output_path` |
| POST | `/api/v1/ffmpeg/probe` | **同步** | ffprobe 探测，返回时长/宽高/流信息 |
| GET  | `/healthz` | 同步 | 健康检查 |

### 支持的操作（operation 字段）

| operation | 说明 | 关键输入参数 |
|-----------|------|-------------|
| `mix-audio` | TTS + BGM 混音 | `tts_path`, `bgm_path`, `tts_volume`, `bgm_volume`, `duration_sec` |
| `concat-audio` | 多段音频拼接 | `audio_paths[]`, `output_format` |
| `concat-video` | 多段视频拼接 | `video_paths[]`, `reencode` |
| `frames-to-video` | 帧序列合成视频 | `frame_dir`, `frame_pattern`, `fps`, `width`, `height` |
| `image-to-frames` | 图片转帧序列 | `image_path`, `fps`, `duration_sec`, `output_dir` |
| `merge-av` | 视频 + 音轨合并 | `video_path`, `audio_path`, `shortest` |
| `burn-subtitle` | 字幕烧录 | `video_path`, `subtitle_path`, `style_override` |
| `extract-frame` | 提取指定位置帧 | `video_path`, `position`(head/tail/time), `time_sec` |
| `postprocess` | 水印/drawtext | `video_path`, `watermark_text`, `watermark_image_path`, `drawtext_params` |
| `image-preprocess` | 图片缩放/裁剪 | `image_path`, `target_width`, `target_height`, `fit_mode`(cover/contain/fill) |

### 主项目需新增文件（FFmpeg）

| 文件 | 说明 |
|------|------|
| `pkg/ffmpeg/providers/ffmpeg_service_provider.go` | **新增**（见 `docs/output/ffmpeg_service_provider.go`） |

主系统各工具改造示例：

```go
// 原来（直接 exec）
cmd := exec.CommandContext(ctx, "ffmpeg", args...)

// 改为（HTTP 提交 + 轮询）
jobID, err := ffmpegProvider.SubmitMixAudio(ctx, providers.MixAudioParams{
    TTSPath: ttsPath, BGMPath: bgmPath, TTSVolume: 1.0, BGMVolume: 0.3,
})
outputPath, _, err := ffmpegProvider.WaitJob(ctx, jobID)

// probe（同步）
info, err := ffmpegProvider.Probe(ctx, audioPath)
durationSec := info.DurationSec
```

### 主系统配置扩展（FFmpeg）

```yaml
ffmpeg:
  service_url: "http://ffmpeg-service:8089"  # 非空 → 使用 FFmpegServiceProvider
  submit_timeout_ms: 1000
  wait_timeout_ms: 300000
  poll_interval_ms: 2000
```

### FFmpeg Service 启动方式

```bash
# 本地开发（需本机安装 ffmpeg）
cd services/ffmpeg-service
cp config.example.yaml config.yaml   # 按需修改 work_dir / redis.addr
go run ./cmd/server

# Docker
docker build -t ffmpeg-service .
docker run -p 8089:8089 \
  -v /data/media:/data/media \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  ffmpeg-service

# docker compose（与主系统共享 media-data volume）
docker compose up ffmpeg-service
```

---

## 4. 目录结构规范

```
dream-ai-tools/
├── AGENTS.md
├── AGENTS.md.go                          ← Go 模块占位
├── docs/
│   ├── ffmpeg-service-prd.md
│   ├── ffmpeg-service-technical-design.md
│   ├── tts-service-prd.md
│   ├── tts-service-technical-design.md
│   ├── tts-service-task-breakdown.md
│   └── output/
│       ├── edge_service_provider.go      ← 复制到 ai-engine/pkg/tts/providers/
│       ├── ffmpeg_service_provider.go    ← 复制到 ai-engine/pkg/ffmpeg/providers/
│       ├── main-docker-compose.yml
│       └── server_patch.md
└── services/
    ├── tts-service/                      ← Gin TTS Service（Go，端口 8088）
    │   ├── cmd/server/main.go
    │   ├── internal/
    │   │   ├── api/handler.go            ← POST /api/v1/tts, GET /api/v1/tts/result
    │   │   ├── task/                     ← Task 模型、MemoryStore、RedisStore
    │   │   └── worker/client.go          ← HTTP 调 Python Worker
    │   ├── config.example.yaml
    │   ├── Dockerfile
    │   └── go.mod
    ├── tts-worker/                       ← Python FastAPI Worker（端口 8090）
    │   ├── app/main.py                   ← POST /api/v1/synthesize, GET /healthz
    │   ├── requirements.txt
    │   └── README.md
    └── ffmpeg-service/                   ← Gin FFmpeg Service（Go，端口 8089）✅
        ├── cmd/server/main.go
        ├── internal/
        │   ├── api/handler.go            ← POST /jobs, GET /jobs/result, POST /probe
        │   ├── job/                      ← Job 模型、MemoryStore、RedisStore
        │   └── executor/                 ← 10 个操作 executor + probe
        ├── config.example.yaml
        ├── Dockerfile
        └── go.mod
```

---

## 5. 开发任务清单

### TTS P0 — MVP

| ID | 任务 | 目标文件 | 状态 |
|----|------|----------|------|
| P0-1 | 冻结 API 契约 | docs/ | ✅ |
| P0-2 | Python edge-tts Worker | `services/tts-worker/` | ✅ |
| P0-3 | Gin TTS Service | `services/tts-service/` | ✅ |
| P0-4 | 主系统 EdgeServiceProvider | `ai-engine/pkg/tts/providers/` | ✅ docs/output 已生成 |
| P0-5 | 主系统配置扩展 | `config/config.go` + `config.example.yaml` | ⬜ |
| P0-6 | 本地联调 | — | ⬜ |

### TTS P1 — 生产化

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| P1-1 | Redis 状态持久化 | 替换内存 map | ✅ |
| P1-2 | OSS 上传 | Worker 上传，返回 URL | ⬜ |
| P1-3 | 结构化可观测性 | task_id / voice / chars / latency 日志 | ✅ |
| P1-4 | Docker + Compose | 各自 Dockerfile；compose 示例 | ✅ |

### FFmpeg P0 — MVP

| ID | 任务 | 目标文件 | 状态 |
|----|------|----------|------|
| F0-1 | 冻结 API 契约 | docs/ | ✅ |
| F0-2 | FFmpeg Service（10 操作 + probe） | `services/ffmpeg-service/` | ✅ |
| F0-3 | 主系统 FFmpegServiceProvider | `ai-engine/pkg/ffmpeg/providers/` | ✅ docs/output 已生成 |
| F0-4 | 主系统配置扩展 | `ffmpeg.service_url` 配置开关 | ⬜ |
| F0-5 | 逐工具替换 exec.Command | 11 个工具文件 | ⬜ |
| F0-6 | 主系统 Dockerfile 移除 ffmpeg | — | ⬜ |

### FFmpeg P1 — 生产化

| ID | 任务 | 说明 | 状态 |
|----|------|------|------|
| F1-1 | Redis 状态持久化 | 复用 tts-service 模式（db=1） | ⬜ |
| F1-2 | Docker + Compose | ffmpeg-service Dockerfile；media-data volume | ✅ |

---

## 6. 主项目参考文件路径

```
/Users/xiaoyuan/Documents/work/git/dream-ai-tts/ai-engine/
├── pkg/tts/
│   ├── provider.go          ← Provider / AsyncProvider / Service 接口定义
│   ├── types.go             ← SynthesizeRequest / SynthesizeResult 等全部类型
│   └── providers/
│       └── edge_provider.go ← 现有 CLI provider，edge_service_provider.go 参考此结构
└── server/server.go:103     ← TTS provider 初始化，需在此加 service_url 分支
```

关键 TTS 类型速查：

| 类型 | 说明 |
|------|------|
| `tts.SynthesizeRequest` | Text / Voice / Rate / Volume / Pitch / Format / Mode / TaskID … |
| `tts.SynthesizeResult` | AudioLocalPath / DurationSec / Provider / Chars / Protocol / JobID … |
| `tts.SubmitSynthesizeResult` | Provider / Protocol / Status / JobID … |
| `tts.WaitSynthesizeRequest` | Provider / JobID / WaitTimeout / PollInterval … |
| `tts.ProviderEdge` | `ProviderName = "edge"` |
| `tts.TransportProtocolAsync` | `TransportProtocol = "async"` |

---

## 7. 注意事项

### 跨容器文件访问（MVP 关键风险）

- TTS：Worker 写 `/data/tts/audio/{task_id}.mp3`，主系统 ffprobe 须能读到此路径 → 挂同一 volume。
- FFmpeg：输入/输出均在 `/data/media/`，主系统与 ffmpeg-service 须挂同一 `media-data` volume。
- P1 接入 OSS 后，两个问题均消除，主系统改用 URL 字段。

### FFmpeg 任务耗时

视频操作可达数十秒，`wait_timeout_ms` 默认 300 000 ms（5 分钟）；`poll_interval_ms` 默认 2 000 ms。

### 任务状态线程安全

两个 Service 的内存 store 均用 `sync.RWMutex`；RedisStore 用 Watch/TxPipelined 乐观锁。

### Redis DB 隔离

tts-service 使用 `db: 0`，ffmpeg-service 使用 `db: 1`，避免 key 冲突。

### Worker API 不对外暴露

`POST /api/v1/synthesize`（tts-worker）只允许 tts-service 调用，生产环境通过 Docker network 隔离。

### 配置兼容策略

```yaml
# service_url 非空 → 使用 HTTP provider（服务化）
# service_url 为空 → 保留旧 CLI 调用（本地开发降级）
tts:
  edge:
    service_url: "http://tts-service:8088"

ffmpeg:
  service_url: "http://ffmpeg-service:8089"
```
