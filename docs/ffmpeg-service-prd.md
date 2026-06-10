# FFmpeg 服务化改造 PRD

日期：2026-05-03

状态：Draft

关联文档：

- [FFmpeg 服务化技术设计](./ffmpeg-service-technical-design.md)

---

## 1. 背景

当前 AI Engine 在主进程中通过 `exec.Command("ffmpeg")` / `exec.Command("ffprobe")` 直接调用本地二进制，涉及 11 类操作（混音、拼接、字幕烧录、帧提取等）。这导致：

1. 主系统 Docker 镜像必须安装 ffmpeg，镜像体积显著增大（+100MB 以上）。
2. ffmpeg 版本与主系统部署生命周期绑定，升级困难。
3. 所有 ffmpeg 操作在主进程内同步阻塞执行，影响工作流并发吞吐。
4. 后续替换为云端转码 API（阿里云媒体处理、AWS MediaConvert 等）时需修改主系统执行逻辑。

问题本质与 TTS 服务化相同：

```text
媒体处理能力未服务化，导致主系统镜像臃肿、扩展困难。
```

---

## 2. 产品目标

将 FFmpeg 能力升级为独立服务：

```text
Go Workflow Engine
        |
        | HTTP（submit + wait 异步）
        v
Go FFmpeg Service
        |
        | exec.Command
        v
ffmpeg / ffprobe 二进制
        |
        v
共享 Volume 上的媒体文件
```

目标：

1. 主系统镜像移除 ffmpeg / ffprobe 依赖，体积减小。
2. 媒体处理任务异步执行，不阻塞主工作流调度线程。
3. 提交接口响应时间小于 100ms。
4. 支持并发任务与失败重试。
5. 保留未来替换云端转码 API 的扩展接口。

---

## 3. 非目标

1. 不实现通用 `ffmpeg -i xxx` 透传接口，只封装业务操作。
2. 不在本期实现云端转码 API 对接。
3. 不实现多 FFmpeg Service 实例的负载均衡（P2）。
4. MVP 不上传 OSS，文件通过共享 Volume 传递路径。

---

## 4. 用户与使用场景

### 4.1 AI Engine 工作流节点

以下工具节点改为通过 HTTP 调用 FFmpeg Service：

| 工具名 | 操作 |
|--------|------|
| `mix_audio` | mix-audio |
| `merge_video` | concat-video |
| `frames_to_video` | frames-to-video |
| `image_to_frames` | image-to-frames |
| `assemble_voiceover_audio` | concat-audio |
| `video_assemble_pro` | merge-av |
| `video_subtitle_burn_v2` | burn-subtitle |
| `video_tail_frame_extract` | extract-frame |
| `video_cover_extract` | extract-frame |
| `video_postprocess` | postprocess |
| `image_preprocess` | image-preprocess |
| `ProbeAudioDuration` / 各类 ffprobe | probe |

### 4.2 运维部署

运维只需保证：

- 主系统可访问 FFmpeg Service。
- FFmpeg Service 与主系统挂载同一个共享 Volume。
- 主系统镜像不再安装 ffmpeg。

---

## 5. 功能需求

### 5.1 操作列表

#### ffmpeg 写操作（异步，支持 submit + wait）

| operation | 说明 | 主要输入 | 输出 |
|-----------|------|----------|------|
| `mix-audio` | TTS 音频与 BGM 混音 | tts_path, bgm_path, 音量比例 | 混音 mp3/m4a 路径 |
| `concat-audio` | 多段音频按顺序拼接 | audio_paths[], 间隔时长 | 拼接音频路径 |
| `concat-video` | 多段视频无缝拼接 | video_paths[] | 拼接视频路径 |
| `frames-to-video` | 图片帧序列合成视频 | frame_dir, fps, 分辨率 | 视频路径 |
| `image-to-frames` | 图片转为帧序列 | image_path, fps, 时长 | 帧目录路径 |
| `merge-av` | 视频与音频轨合并 | video_path, audio_path | 合并视频路径 |
| `burn-subtitle` | 将字幕文件烧录进视频 | video_path, subtitle_path, 样式参数 | 烧录视频路径 |
| `extract-frame` | 提取视频指定位置帧 | video_path, position(head/tail/time_sec) | 帧图片路径 |
| `postprocess` | 水印/drawtext 叠加 | video_path, 水印参数 | 处理后视频路径 |
| `image-preprocess` | 图片缩放/裁剪/填充 | image_path, 目标分辨率, fit模式 | 处理后图片路径 |
| `detect-scenes` | 检测视频画面切换点 | video_path, threshold, min_scene_duration | scenes[] |

#### ffprobe 只读操作（同步返回）

| operation | 说明 | 输出 |
|-----------|------|------|
| `probe` | 获取媒体文件元信息 | duration_sec, width, height, streams[] |

### 5.2 任务状态

与 TTS Service 保持一致：

| 状态 | 含义 |
|------|------|
| `processing` | 任务已创建，正在执行 |
| `done` | 执行完成 |
| `failed` | 执行失败 |

### 5.3 API

#### 提交任务

```http
POST /api/v1/ffmpeg/jobs
Content-Type: application/json
```

请求：

```json
{
  "operation": "mix-audio",
  "params": {
    "tts_path": "/data/media/tts_abc.mp3",
    "bgm_path": "/data/media/bgm.mp3",
    "tts_volume": 1.0,
    "bgm_volume": 0.3,
    "duration_sec": 10.5
  }
}
```

响应：

```json
{
  "job_id": "ffmpeg_01KQMHP5CK...",
  "status": "processing"
}
```

#### 查询结果

```http
GET /api/v1/ffmpeg/jobs/result?id={job_id}
```

完成响应：

```json
{
  "job_id": "ffmpeg_01KQMHP5CK...",
  "status": "done",
  "output_path": "/data/media/ffmpeg_01KQMHP5CK....mp3"
}
```

失败响应：

```json
{
  "job_id": "ffmpeg_01KQMHP5CK...",
  "status": "failed",
  "error_code": "ffmpeg_failed",
  "error_message": "exit status 1: ..."
}
```

#### probe 同步接口

```http
POST /api/v1/ffmpeg/probe
Content-Type: application/json
```

请求：

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
  "streams": [
    {"codec_type": "video", "codec_name": "h264"},
    {"codec_type": "audio", "codec_name": "aac"}
  ]
}
```

#### 健康检查

```http
GET /healthz
→ {"status": "ok"}
```

---

## 6. 非功能需求

1. 提交任务接口 P95 响应小于 100ms。
2. probe 接口 P95 响应小于 500ms。
3. 单任务失败后重试 1 次，重试次数可配置。
4. 任务状态更新线程安全。
5. 日志包含 `job_id / operation / status / latency_ms / error_code`。
6. FFmpeg Service 重启后任务状态可从 Redis 恢复（P1）。

---

## 7. MVP 范围

MVP 必须包含：

1. FFmpeg Service（Go + Gin）。
2. 所有 10 个 ffmpeg 写操作。
3. probe 同步接口。
4. 共享 Volume 文件路径传递。
5. 内存任务状态存储（可选接 Redis，复用 tts-service 模式）。
6. 主系统新增 HTTP FFmpeg Provider 替代直接 exec.Command。

MVP 可暂缓：

1. Redis 任务持久化（可直接复用 tts-service 的 RedisStore）。
2. 云端转码 API 对接。
3. 多实例负载均衡。

---

## 8. 验收标准

1. 主系统镜像 Dockerfile 中移除 ffmpeg / ffprobe 安装。
2. 所有工作流节点通过 HTTP 获取媒体处理结果。
3. 提交任务接口响应小于 100ms。
4. probe 接口可正确返回时长、宽高。
5. 任务失败时错误原因可查询。
6. 商品视频完整链路（TTS + 混音 + 视频合并 + 字幕）可端到端跑通。
