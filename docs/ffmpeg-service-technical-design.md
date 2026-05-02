# FFmpeg 服务化技术设计

日期：2026-05-03

状态：Draft

关联文档：

- [FFmpeg 服务化改造 PRD](./ffmpeg-service-prd.md)

---

## 1. 当前代码基线

主项目中 ffmpeg/ffprobe 调用分布如下：

| 文件 | exec.Command 次数 | 说明 |
|------|-------------------|------|
| `tool/builtin/mix_audio.go` | 1 | ffmpeg 混音 |
| `tool/builtin/merge_video.go` | 2 | ffmpeg 拼接（含重编码回退） |
| `tool/builtin/frames_to_video.go` | 2 | ffmpeg 帧合成视频 |
| `tool/builtin/image_to_frames.go` | 1 | ffmpeg 图片转帧 |
| `workflows/goods/assemble_voiceover_audio.go` | 1 | ffmpeg 音频拼接 |
| `workflows/goods/video_assemble_pro.go` | 3+1 | ffmpeg 合成 + ffprobe 探测 |
| `workflows/goods/video_subtitle_burn_v2.go` | 1+1 | ffmpeg 烧录 + ffprobe 时长 |
| `workflows/goods/video_tail_frame_extract.go` | 1+1 | ffmpeg 提帧 + ffprobe 时长 |
| `workflows/videos/video_cover_extract.go` | 1 | ffmpeg 提封面帧 |
| `workflows/videos/video_postprocess.go` | 2+1 | ffmpeg 后处理 + ffprobe 探测 |
| `workflows/videos/image_preprocess.go` | 1+1 | ffmpeg 预处理 + ffprobe 探测 |
| `pkg/tts/util.go` | 1 | ffprobe 音频时长（所有 TTS provider 使用） |

所有调用模式一致：构建 args → exec.CommandContext → 解析 stdout/stderr。本次改造无需重新设计执行模型，只需将 exec 调用迁移到 FFmpeg Service 内部。

---

## 2. 总体架构

```text
ai-engine main process
  |
  | FFmpegServiceProvider（HTTP client）
  v
FFmpeg Service（Go + Gin，端口 8089）
  |
  | exec.Command("ffmpeg") / exec.Command("ffprobe")
  v
ffmpeg / ffprobe 二进制（只在此容器内安装）
  |
  v
/data/media/（共享 Volume）
```

职责边界：

| 模块 | 职责 | 不负责 |
|------|------|--------|
| AI Engine | 工作流编排、文件路径管理 | 执行 ffmpeg |
| FFmpegServiceProvider | 把现有 exec 调用转成 HTTP 任务 | 处理媒体文件内容 |
| FFmpeg Service | API、任务调度、重试、exec.Command | 业务逻辑 |
| 共享 Volume | 媒体文件存储 | 任务状态管理 |

---

## 3. 目录结构

```text
services/ffmpeg-service/
  cmd/server/main.go
  internal/
    api/
      handler.go          # POST /jobs, GET /jobs/result, POST /probe
    job/
      job.go              # Job 模型（复用 tts task 模式）
      store.go            # Store 接口
      memory_store.go
      redis_store.go      # 直接复用 tts-service 实现
    executor/
      executor.go         # 统一入口：按 operation 分派
      mix_audio.go
      concat_audio.go
      concat_video.go
      frames_to_video.go
      image_to_frames.go
      merge_av.go
      burn_subtitle.go
      extract_frame.go
      postprocess.go
      image_preprocess.go
      probe.go
  config.example.yaml
  Dockerfile
  go.mod
```

---

## 4. 核心数据结构

### Job 模型

```go
type Status string

const (
    StatusProcessing Status = "processing"
    StatusDone       Status = "done"
    StatusFailed     Status = "failed"
)

type Job struct {
    ID           string
    Operation    string
    Params       map[string]any
    Status       Status
    OutputPath   string         // 单输出文件路径
    OutputPaths  []string       // 多输出文件路径（如帧序列）
    ErrorCode    string
    ErrorMessage string
    RetryCount   int
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

### 请求/响应

提交任务请求（通用结构，params 按 operation 变化）：

```go
type SubmitRequest struct {
    Operation string         `json:"operation"`
    Params    map[string]any `json:"params"`
}

type SubmitResponse struct {
    JobID  string `json:"job_id"`
    Status string `json:"status"`
}

type ResultResponse struct {
    JobID        string   `json:"job_id"`
    Status       string   `json:"status"`
    OutputPath   string   `json:"output_path,omitempty"`
    OutputPaths  []string `json:"output_paths,omitempty"`
    ErrorCode    string   `json:"error_code,omitempty"`
    ErrorMessage string   `json:"error_message,omitempty"`
}
```

---

## 5. 各操作参数规范

### mix-audio

```json
{
  "tts_path": "/data/media/tts.mp3",
  "bgm_path": "/data/media/bgm.mp3",
  "tts_volume": 1.0,
  "bgm_volume": 0.3,
  "duration_sec": 10.5,
  "output_format": "mp3"
}
```

ffmpeg 命令参考：
```
ffmpeg -i tts.mp3 -i bgm.mp3
  -filter_complex "[0]volume=1.0[a];[1]volume=0.3,atrim=0:10.5[b];[a][b]amix=inputs=2:duration=first"
  -t 10.5 output.mp3
```

### concat-audio

```json
{
  "audio_paths": ["/data/media/seg1.mp3", "/data/media/seg2.mp3"],
  "gap_sec": 0.0,
  "output_format": "mp3"
}
```

### concat-video

```json
{
  "video_paths": ["/data/media/v1.mp4", "/data/media/v2.mp4"],
  "reencode": false
}
```

使用 ffmpeg concat demuxer（无损拼接），`reencode=true` 时用 filter_complex。

### frames-to-video

```json
{
  "frame_dir": "/data/media/frames/",
  "frame_pattern": "frame_%04d.jpg",
  "fps": 25,
  "width": 1920,
  "height": 1080,
  "output_format": "mp4"
}
```

### image-to-frames

```json
{
  "image_path": "/data/media/image.jpg",
  "fps": 25,
  "duration_sec": 3.0,
  "output_dir": "/data/media/frames_out/"
}
```

### merge-av

```json
{
  "video_path": "/data/media/video.mp4",
  "audio_path": "/data/media/audio.mp3",
  "shortest": true
}
```

### burn-subtitle

```json
{
  "video_path": "/data/media/video.mp4",
  "subtitle_path": "/data/media/subtitle.ass",
  "style_override": ""
}
```

### extract-frame

```json
{
  "video_path": "/data/media/video.mp4",
  "position": "tail",
  "time_sec": null,
  "output_format": "jpg"
}
```

`position` 枚举：`head`（第0秒）/ `tail`（最后一帧）/ `time`（`time_sec` 指定）。

### postprocess

```json
{
  "video_path": "/data/media/video.mp4",
  "watermark_text": "",
  "watermark_image_path": "",
  "drawtext_params": {}
}
```

### image-preprocess

```json
{
  "image_path": "/data/media/image.jpg",
  "target_width": 1920,
  "target_height": 1080,
  "fit_mode": "cover",
  "output_format": "jpg"
}
```

`fit_mode` 枚举：`cover` / `contain` / `fill`。

### probe（同步）

```json
{
  "path": "/data/media/video.mp4"
}
```

响应：

```json
{
  "duration_sec": 12.34,
  "width": 1920,
  "height": 1080,
  "size_bytes": 10485760,
  "streams": [
    {"codec_type": "video", "codec_name": "h264", "fps": 25},
    {"codec_type": "audio", "codec_name": "aac", "sample_rate": 44100}
  ]
}
```

---

## 6. 任务调度模型

与 tts-service 完全一致：

```text
POST /api/v1/ffmpeg/jobs
  → validate operation + params
  → create job（status=processing）
  → store.Save(job)
  → go executor.Run(job)     ← goroutine 异步执行
  → 立即返回 job_id
```

执行器：

```go
type Executor interface {
    Run(ctx context.Context, job *job.Job) (outputPath string, err error)
}

// 分派表
var executors = map[string]Executor{
    "mix-audio":        &MixAudioExecutor{},
    "concat-audio":     &ConcatAudioExecutor{},
    "concat-video":     &ConcatVideoExecutor{},
    "frames-to-video":  &FramesToVideoExecutor{},
    "image-to-frames":  &ImageToFramesExecutor{},
    "merge-av":         &MergeAVExecutor{},
    "burn-subtitle":    &BurnSubtitleExecutor{},
    "extract-frame":    &ExtractFrameExecutor{},
    "postprocess":      &PostprocessExecutor{},
    "image-preprocess": &ImagePreprocessExecutor{},
}
```

---

## 7. 主系统 FFmpegServiceProvider 设计

新增文件（输出到 `docs/output/ffmpeg_service_provider.go`，手动复制到主项目）：

```go
type FFmpegServiceProvider struct {
    baseURL      string
    submitClient *http.Client
    pollClient   *http.Client
    pollInterval time.Duration
    waitTimeout  time.Duration
}
```

接口与 TTS provider 平行，主系统工具层改造：

```go
// 原来
cmd := exec.CommandContext(ctx, "ffmpeg", args...)

// 改为
result, err := ffmpegProvider.Submit(ctx, "mix-audio", params)
outputPath, err := ffmpegProvider.Wait(ctx, result.JobID)
```

对于 probe：

```go
// 同步调用
info, err := ffmpegProvider.Probe(ctx, path)
```

---

## 8. 文件路径约定

```text
/data/media/
  input/   ← 主系统写入输入文件（从 OSS 下载或工作流生成）
  output/  ← FFmpeg Service 写入输出文件
```

MVP：主系统和 FFmpeg Service 挂同一个 Docker Volume `media-data` 到 `/data/media`。

生产：输入输出都走 OSS URL，Volume 不再必要。

---

## 9. 配置设计

FFmpeg Service：

```yaml
server:
  port: 8089

executor:
  work_dir: "/data/media/output"
  ffmpeg_path: "ffmpeg"
  ffprobe_path: "ffprobe"
  max_concurrent: 4
  retry_times: 1
  timeout_ms: 300000

redis:
  addr: ""        # 为空使用内存 store
  password: ""
  db: 1           # 与 tts-service 用不同 db 隔离
```

主系统配置扩展：

```yaml
ffmpeg:
  service_url: "http://ffmpeg-service:8089"
  submit_timeout_ms: 1000
  wait_timeout_ms: 300000
  poll_interval_ms: 2000
```

---

## 10. docker-compose 变化

```yaml
ffmpeg-service:
  build: ./ffmpeg-service
  restart: unless-stopped
  volumes:
    - media-data:/data/media
    - ./ffmpeg-service/config.yaml:/app/config.yaml:ro
  networks:
    - tts-net          # 复用已有网络，统一命名为 dream-ai-services-net（可重命名）
  ports:
    - "8089:8089"
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost:8089/healthz"]
    interval: 15s
    timeout: 5s
    retries: 3

volumes:
  media-data:          # 主系统和 ffmpeg-service 共享
```

主系统 docker-compose 同步加入 `media-data` volume 挂载。

---

## 11. 迁移策略

### 阶段 1：文档与契约冻结（本文档）

### 阶段 2：实现 FFmpeg Service

- 实现 10 个 executor。
- 实现 probe 同步接口。
- 复用 tts-service 的 task/store/handler 模式。

### 阶段 3：主系统接入 HTTP Provider

- 新增 `FFmpegServiceProvider`。
- 逐工具替换 exec.Command 调用。
- 通过 `ffmpeg.service_url` 配置切换。

### 阶段 4：主系统 Dockerfile 移除 ffmpeg

- 移除 `apt-get install ffmpeg`。
- 验证所有工作流节点通过 HTTP 正常工作。

---

## 12. 风险与对策

| 风险 | 影响 | 对策 |
|------|------|------|
| 视频文件大，路径传递要求共享 Volume | MVP 必须挂共享卷 | docker-compose 统一挂 media-data volume |
| ffmpeg 操作耗时长（数十秒） | wait 轮询时间长 | wait_timeout_ms 默认 300s；异步不阻塞提交 |
| 操作参数复杂，各 executor 参数不一致 | params 校验难 | 每个 executor 内部做强类型解析和校验 |
| Service 重启丢失进行中任务 | MVP 接受 | P1 接入 Redis（直接复用 tts-service RedisStore） |
| 主系统工具层改造量大（11 个工具） | 改造周期长 | 按工作流优先级分批替换，旧工具保留配置降级路径 |

---

## 13. 技术结论

1. FFmpeg Service 架构与 tts-service 完全平行，直接复用 job/store/handler 模式。
2. probe 操作同步返回，不走 job 队列。
3. MVP 用共享 Volume 传路径，无需实现文件上传接口。
4. 主系统改造量最大（11 个工具），建议按商品视频链路优先级分批替换。
5. 最终主系统 Dockerfile 移除 ffmpeg 后，镜像体积预计减少 80-150MB。
