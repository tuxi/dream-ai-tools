# dream-ai-tools 部署文档

适用于 Ubuntu 云服务器，与主项目 `dream-ai` 平行部署。

## 1. 服务概览

| 服务 | 语言 | 端口 | 说明 |
|------|------|------|------|
| ffmpeg-service | Go | 8089 | 异步 ffmpeg 操作 HTTP API，含 ffprobe |
| tts-service | Go | 8088 | 异步 TTS 任务 HTTP API |
| tts-worker | Python | 8090（内部） | 实际运行 edge-tts，仅 tts-service 调用 |
| ocr-service | Python | 8091 | PaddleOCR keyframes 文字识别 HTTP API |
| asr-service | Python | 8092 | FunASR 音视频转写 HTTP API |
| redis | - | 内部 | ffmpeg-service 用 db=1，tts-service 用 db=0 |

**主项目访问方式：**
- `http://127.0.0.1:8088` → tts-service
- `http://127.0.0.1:8089` → ffmpeg-service
- `http://127.0.0.1:8091` → ocr-service
- `http://127.0.0.1:8092` → asr-service

**路径一致性原则：** tts-worker 保存的音频路径，ffmpeg-service / ocr-service / asr-service 读取的 media/tmp 路径，都使用宿主机绝对路径，容器内通过 bind mount 挂载相同路径。主项目在宿主机上可直接读取这些路径的文件。

## 2. GitHub Actions 镜像构建

工作流文件：`/.github/workflows/docker-publish.yml`

推送到 `main` 分支或打 tag 时自动构建并推送 5 个镜像到 GHCR：

```
ghcr.io/tuxi/dream-ai-tools/ffmpeg-service:latest
ghcr.io/tuxi/dream-ai-tools/tts-service:latest
ghcr.io/tuxi/dream-ai-tools/tts-worker:latest
ghcr.io/tuxi/dream-ai-tools/ocr-service:latest
ghcr.io/tuxi/dream-ai-tools/asr-service:latest
```

首次确认：
1. GitHub Actions 页面构建成功
2. 5 个 GHCR 包已生成
3. 如果包是 private，服务器需用 GitHub PAT 登录

服务器登录 GHCR：
```bash
echo YOUR_GITHUB_PAT | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

### 国内服务器建议：同步推送到国内镜像仓库

腾讯云服务器不建议直接拉 GHCR 的 ASR 镜像。`asr-service` 镜像包含 `torch/torchaudio/funasr`，镜像层可达 GB 级；从 GHCR 拉取可能非常慢，甚至无法完成。

建议把 GitHub Actions 产物同步推送到阿里云 ACR 或腾讯云 TCR/CCR，然后服务器 `.env` 使用国内 registry。

如果阿里云 ACR 是单仓库，例如：

```text
crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools
```

GitHub Variables 配置为：

```text
ACR_REGISTRY=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com
ACR_NAMESPACE=dreamlog
ACR_REPOSITORY=dream-ai-tools
```

发布后会使用单仓库多 tag：

```text
crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:ffmpeg-service-latest
crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:tts-service-latest
crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:tts-worker-latest
crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:ocr-service-latest
crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:asr-service-latest
```

服务器 `.env` 使用显式镜像变量：

```env
FFMPEG_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:ffmpeg-service-latest
TTS_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:tts-service-latest
TTS_WORKER_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:tts-worker-latest
OCR_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:ocr-service-latest
ASR_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:asr-service-latest
```

如果是每个服务一个仓库，则继续使用镜像前缀：

```env
TOOLS_IMAGE_PREFIX=registry.cn-beijing.aliyuncs.com/<namespace>
# 或：
TOOLS_IMAGE_PREFIX=ccr.ccs.tencentyun.com/<namespace>/dream-ai-tools
```

当前 workflow 的 ACR 变量也可用于任意 Docker Registry：

在 GitHub 仓库 Settings 中添加：

Variables：
- `ACR_REGISTRY`（如 `registry.cn-beijing.aliyuncs.com`、企业版实例域名或 `ccr.ccs.tencentyun.com`）
- `ACR_NAMESPACE`（阿里云个人版建议直接填 `<namespace>`；其他 registry 如需分组可填 `<namespace>/dream-ai-tools`）
- `ACR_REPOSITORY`（可选；配置后启用单仓库多 tag 模式，如 `dream-ai-tools`）

Secrets：
- `ACR_USERNAME`
- `ACR_PASSWORD`

未配置时自动跳过，不影响 GHCR 推送。

## 3. 服务器目录结构

```text
~/apps/dream-ai-tools/
  compose.yml             # 从仓库同步
  .env                    # 服务器本地，不进 git
  config/
    ffmpeg-service.yaml   # 服务器本地，不进 git
    tts-service.yaml      # 服务器本地，不进 git
    ocr-service.yaml      # 服务器本地，不进 git
    asr-service.yaml      # 服务器本地，不进 git
  data/
    redis/                # Redis 持久化数据
    tts/
      audio/              # TTS 音频文件（主项目也会读取此目录）
    media/                # ffmpeg 工作目录（主项目也会读取此目录）
    tmp/                  # OCR / ASR / 主项目共享临时目录
    models/               # OCR / ASR 模型持久化缓存
	    fonts/                # 字幕字体文件，挂载到 ffmpeg-service /fonts
```

创建目录：

```bash
mkdir -p ~/apps/dream-ai-tools/config
mkdir -p ~/apps/dream-ai-tools/data/redis
mkdir -p ~/apps/dream-ai-tools/data/tts/audio
mkdir -p ~/apps/dream-ai-tools/data/media
mkdir -p ~/apps/dream-ai-tools/data/tmp
mkdir -p ~/apps/dream-ai-tools/data/models
mkdir -p ~/apps/dream-ai-tools/data/fonts
```

## 4. 从仓库同步 compose 文件

```bash
# 首次
git clone --depth=1 git@github.com:tuxi/dream-ai-tools.git ~/apps/dream-ai-tools-src

# 复制 compose 文件到运行目录
cp ~/apps/dream-ai-tools-src/services/docker-compose.yml ~/apps/dream-ai-tools/compose.yml

# 后续更新
cd ~/apps/dream-ai-tools-src && git pull
cp services/docker-compose.yml ~/apps/dream-ai-tools/compose.yml
```

## 5. 创建 .env 文件

```bash
cat > ~/apps/dream-ai-tools/.env <<'EOF'
TOOLS_IMAGE_PREFIX=ghcr.io/tuxi/dream-ai-tools
IMAGE_TAG=latest

# 阿里云 ACR 单仓库多 tag 模式时取消注释，并替换 registry/namespace/repository。
# FFMPEG_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:ffmpeg-service-latest
# TTS_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:tts-service-latest
# TTS_WORKER_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:tts-worker-latest
# OCR_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:ocr-service-latest
# ASR_SERVICE_IMAGE=crpi-j6saptuqjsv9vt36.cn-beijing.personal.cr.aliyuncs.com/dreamlog/dream-ai-tools:asr-service-latest

DATA_PATH=/home/ubuntu/apps/dream-ai-tools/data
CONFIG_PATH=/home/ubuntu/apps/dream-ai-tools/config
REDIS_DATA_PATH=/home/ubuntu/apps/dream-ai-tools/data/redis

REDIS_PASSWORD=change-this-redis-password

TTS_SERVICE_PORT=8088
FFMPEG_SERVICE_PORT=8089
OCR_SERVICE_PORT=8091
ASR_SERVICE_PORT=8092
EOF
```

**重要：** 把 `change-this-redis-password` 改成真实密码，并与下方 config 文件中的密码保持一致。

## 6. 创建服务配置文件

### ffmpeg-service.yaml

```bash
cat > ~/apps/dream-ai-tools/config/ffmpeg-service.yaml <<'EOF'
server:
  port: 8089

executor:
  work_dir: "/home/ubuntu/apps/dream-ai-tools/data/media"
  ffmpeg_path: "ffmpeg"
  ffprobe_path: "ffprobe"
  max_concurrent: 4
  retry_times: 1
  timeout_ms: 300000

redis:
  addr: "tools-redis:6379"
  password: "change-this-redis-password"
  db: 1
EOF
```

### tts-service.yaml

```bash
cat > ~/apps/dream-ai-tools/config/tts-service.yaml <<'EOF'
server:
  port: 8088

worker:
  base_url: "http://tools-tts-worker:8090"
  timeout_ms: 120000
  retry_times: 1

redis:
  addr: "tools-redis:6379"
  password: "change-this-redis-password"
  db: 0
EOF
```

两个文件中的 `redis.password` 必须与 `.env` 里的 `REDIS_PASSWORD` 一致。

### ocr-service.yaml

```bash
cat > ~/apps/dream-ai-tools/config/ocr-service.yaml <<'EOF'
server:
  host: 0.0.0.0
  port: 8091

ocr:
  engine: paddleocr
  lang: ch
  use_gpu: false
  ocr_version: PP-OCRv5
  model_cache_dir: /models/paddlex
  offline: false
  min_confidence: 0.6
  max_images_per_request: 120
  max_concurrency: 2
  dedupe: true
  dedupe_window_seconds: 2.0

storage:
  allowed_roots:
    - /tmp
    - /home/ubuntu/apps/dream-ai-tools/data/tmp
    - /home/ubuntu/apps/dream-ai-tools/data/media
EOF
```

### asr-service.yaml

```bash
cat > ~/apps/dream-ai-tools/config/asr-service.yaml <<'EOF'
server:
  host: 0.0.0.0
  port: 8092

asr:
  engine: funasr
  language: zh-CN
  use_gpu: false
  model: auto
  model_cache_dir: /models/modelscope
  offline: false
  sample_rate: 16000
  max_duration_seconds: 120
  enable_timestamps: true
  max_concurrency: 1
  work_dir: /tmp/dream-ai-tools/asr
  ffmpeg_path: ffmpeg
  ffprobe_path: ffprobe

storage:
  allowed_roots:
    - /tmp
    - /home/ubuntu/apps/dream-ai-tools/data/tmp
    - /home/ubuntu/apps/dream-ai-tools/data/media
EOF
```

### 模型缓存与离线模式

OCR / ASR 容器会把模型缓存写入 `${DATA_PATH}/models`，容器内路径为 `/models`：

```text
${DATA_PATH}/models/
  cache/
  paddle/
  paddlex/
  paddleocr/
  modelscope/
  huggingface/
  torch/
```

首次上线建议先预下载模型：

```bash
docker run --rm \
  -v /home/ubuntu/apps/dream-ai-tools/data/models:/models \
  -v /home/ubuntu/apps/dream-ai-tools/data/models/paddlex:/root/.paddlex \
  -v /home/ubuntu/apps/dream-ai-tools/config/ocr-service.yaml:/app/config.yaml:ro \
  ${TOOLS_IMAGE_PREFIX}/ocr-service:${IMAGE_TAG:-latest} \
  python scripts/download_models.py --target ocr --config /app/config.yaml

docker run --rm \
  -v /home/ubuntu/apps/dream-ai-tools/data/models:/models \
  -v /home/ubuntu/apps/dream-ai-tools/config/asr-service.yaml:/app/config.yaml:ro \
  ${TOOLS_IMAGE_PREFIX}/asr-service:${IMAGE_TAG:-latest} \
  python scripts/download_models.py --target asr --config /app/config.yaml
```

模型下载完成并确认服务能启动后，可以把 `ocr.offline` / `asr.offline` 改为 `true`。离线模式下服务启动时会检查模型缓存并预加载模型；如果缓存缺失，进程会启动失败并提示先执行模型下载。

## 7. 启动服务

```bash
cd ~/apps/dream-ai-tools

# 拉取默认服务镜像：不包含 ASR（ASR 镜像很大，单独处理）
docker compose -f compose.yml --env-file .env pull

# 启动默认服务：tts / ffmpeg / ocr
docker compose -f compose.yml --env-file .env up -d

# 查看状态
docker compose -f compose.yml --env-file .env ps
```

期望状态：
- `tools-redis` → healthy
- `tools-tts-worker` → healthy（start_period 15s，稍等片刻）
- `tools-tts-service` → healthy
- `tools-ffmpeg-service` → healthy
- `tools-ocr-service` → healthy

ASR 服务默认放在 `asr` profile 中，避免每次部署都拉取 GB 级镜像。需要启用 ASR 时单独执行：

```bash
docker compose -f compose.yml --env-file .env --profile asr pull asr-service
docker compose -f compose.yml --env-file .env --profile asr up -d asr-service
docker compose -f compose.yml --env-file .env --profile asr ps asr-service
```

期望状态：
- `tools-asr-service` → healthy

## 8. 验证服务

```bash
# tts-service
curl http://127.0.0.1:8088/healthz

# ffmpeg-service
curl http://127.0.0.1:8089/healthz

# ocr-service
curl http://127.0.0.1:8091/healthz
curl http://127.0.0.1:8091/readyz

# asr-service
curl http://127.0.0.1:8092/healthz
curl http://127.0.0.1:8092/readyz
```

健康检查接口都应返回 `{"status":"ok"}`。
`/readyz` 在模型已加载后返回 `status=ready`；未加载时返回 503 和 `status=loading`。如果 `offline=true`，服务启动时会预加载模型，readyz 应直接 ready。

快速 TTS 测试：

```bash
curl -s -X POST http://127.0.0.1:8088/api/v1/tts \
  -H "Content-Type: application/json" \
  -d '{"text":"你好世界","voice":"zh-CN-XiaoxiaoNeural"}' | jq .
# 返回 task_id 和 status: processing

# 几秒后轮询结果
curl -s "http://127.0.0.1:8088/api/v1/tts/result?id=<task_id>" | jq .
# status: done，audio_local_path 指向宿主机上的 mp3 文件
```

快速 ffprobe 测试（需要一个可访问的本地文件）：

```bash
curl -s -X POST http://127.0.0.1:8089/api/v1/ffmpeg/probe \
  -H "Content-Type: application/json" \
  -d '{"path":"/home/ubuntu/apps/dream-ai-tools/data/media/test.mp4"}'
```

快速 scene detect 测试（异步任务）：

```bash
curl -s -X POST http://127.0.0.1:8089/api/v1/ffmpeg/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "operation":"detect-scenes",
    "params":{
      "video_path":"/home/ubuntu/apps/dream-ai-tools/data/media/test.mp4",
      "threshold":0.3,
      "min_scene_duration":0.8
    }
  }' | jq .

curl -s "http://127.0.0.1:8089/api/v1/ffmpeg/jobs/result?id=<job_id>" | jq .
```

快速 OCR 测试（异步批量任务，需要一张包含文字的图片）：

```bash
curl -s -X POST http://127.0.0.1:8091/v1/ocr/keyframes/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "task_id":"local-test",
    "language":"zh-CN",
    "keyframes":[
      {
        "id":"kf_001",
        "segment_id":"seg_001",
        "time":0.25,
        "local_path":"/home/ubuntu/apps/dream-ai-tools/data/media/test.jpg"
      }
    ]
  }' | jq .

curl -s "http://127.0.0.1:8091/v1/ocr/keyframes/jobs/result?id=<job_id>" | jq .
```

快速 ASR 测试（需要一个带音频的 mp4）：

```bash
curl -s -X POST http://127.0.0.1:8092/v1/asr/transcribe \
  -H "Content-Type: application/json" \
  -d '{
    "task_id":"local-test",
    "language":"zh-CN",
    "local_video_path":"/home/ubuntu/apps/dream-ai-tools/data/media/test.mp4",
    "with_timestamps":true
  }' | jq .
```

## 9. 主项目 config.yaml 对应配置

主项目 `~/apps/dream-ai/config/config.yaml` 中需要设置：

```yaml
tts:
  enabled: true
  edge:
    service_url: "http://127.0.0.1:8088"
    submit_timeout_ms: 1000
    wait_timeout_ms: 90000

ffmpeg:
  service_url: "http://127.0.0.1:8089"
  submit_timeout_ms: 1000
  wait_timeout_ms: 300000
  poll_interval_ms: 2000
```

## 10. 日常运维

### 查看日志

```bash
cd ~/apps/dream-ai-tools
docker compose -f compose.yml --env-file .env logs -f tts-service
docker compose -f compose.yml --env-file .env logs -f ffmpeg-service
docker compose -f compose.yml --env-file .env logs -f ocr-service
docker compose -f compose.yml --env-file .env logs -f asr-service
docker compose -f compose.yml --env-file .env logs -f tts-worker
docker compose -f compose.yml --env-file .env logs --tail=50 redis
```

### 发版升级

```bash
cd ~/apps/dream-ai-tools
docker compose -f compose.yml --env-file .env pull
docker compose -f compose.yml --env-file .env up -d
docker compose -f compose.yml --env-file .env ps
```

ASR 单独升级：

```bash
docker compose -f compose.yml --env-file .env --profile asr pull asr-service
docker compose -f compose.yml --env-file .env --profile asr up -d asr-service
```

### 重启单个服务

```bash
docker compose -f compose.yml --env-file .env restart tts-service
docker compose -f compose.yml --env-file .env restart ffmpeg-service
```

### 清理旧镜像

```bash
docker image prune -f
```

## 11. 数据安全

数据目录挂载在宿主机：
- `~/apps/dream-ai-tools/data/redis` — Redis 持久化
- `~/apps/dream-ai-tools/data/tts/audio` — TTS 音频（已上传 OSS 的可定期清理）
- `~/apps/dream-ai-tools/data/media` — ffmpeg 工作文件（临时文件，可定期清理）

**严禁执行：**
```bash
docker compose down -v   # 会删除 named volume，虽然当前用 bind mount，但习惯上禁止
```

## 12. 常见问题

### tts-worker 启动慢

Python 镜像 pip 安装较慢，`start_period: 15s`，等 healthcheck 通过后 tts-service 才会启动。如果反复 unhealthy，检查：

```bash
docker compose -f compose.yml --env-file .env logs tts-worker
```

### Redis 连接失败

ffmpeg-service / tts-service 连不上 redis 时会自动降级到内存 store（重启后 job 丢失）。排查：

```bash
docker exec -it tools-redis redis-cli --no-auth-warning -a YOUR_PASSWORD ping
```

确认密码与 `.env` 中 `REDIS_PASSWORD` 一致。

### 音频文件路径不可读

主项目报告音频文件不存在，检查：

1. `~/apps/dream-ai-tools/data/tts/audio/` 目录存在且有写权限
2. `tts-service.yaml` 中 redis 密码正确（否则降级内存 store，重启后任务丢失）
3. `DATA_PATH` 与 `ffmpeg-service.yaml` / `tts-service.yaml` 中路径一致

### 视频烧录字幕不显示（无字幕/空白方块）

**现象：** 带货视频生成任务设置了 `enable_subtitle: true`，output 中 `subtitle_burn_v2` 也正常执行了，但最终视频没有中文字幕，或字幕显示为空白方块（tofu）。

**原因：** ffmpeg-service 的 Docker 镜像基于 Alpine，只装了 ffmpeg 本体，没有中文字体。libass 渲染 ASS 字幕时找不到 `Noto Sans CJK SC` 字体，回退到默认字体，中文字符渲染为空。且 ffmpeg 退出码为 0，`runFFmpeg` 在 2026-05 之前丢弃成功了 stderr，日志无迹可查。

**解决方案（方案 B — 宿主机字体挂载）：**

1. 在宿主机安装字体包，并复制字体到当前服务的数据目录。

```bash
cd ~/apps/dream-ai-tools

DATA_PATH=$(grep '^DATA_PATH=' .env | cut -d= -f2-)
echo "$DATA_PATH"

sudo mkdir -p "$DATA_PATH/fonts"
sudo apt-get update
sudo apt-get install -y fonts-noto-cjk

sudo cp /usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc "$DATA_PATH/fonts/"
sudo chmod 644 "$DATA_PATH/fonts/NotoSansCJK-Regular.ttc"
```

2. `docker-compose.yml` 中 ffmpeg-service 需挂载字体目录：

```yaml
volumes:
  - ${DATA_PATH}/media:${DATA_PATH}/media
  - ${DATA_PATH}/tmp:/tmp
  - ${DATA_PATH}/fonts:/fonts:ro
```

3. 重启 ffmpeg-service，使容器重新挂载字体目录：

```bash
docker compose -f ~/apps/dream-ai-tools/docker-compose.yml --env-file ~/apps/dream-ai-tools/.env restart ffmpeg-service
```

4. 验证容器内字体文件存在：

```bash
docker exec tools-ffmpeg-service sh -lc 'ls -lah /fonts'
```

应看到：

```text
NotoSansCJK-Regular.ttc
```

`fc-list` / `fc-match` 在 Alpine 容器里不一定能列出 bind mount 的字体；最终以 ffmpeg/libass 是否能加载字体为准。可执行最小验证：

```bash
docker exec tools-ffmpeg-service sh -lc '
cat > /tmp/font_test.ass <<EOF
[Script Info]
ScriptType: v4.00+
PlayResX: 720
PlayResY: 1280

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Noto Sans CJK SC,58,&H00FFFFFF,&H00000000,&H40000000,&H00000000,0,0,0,0,100,100,0,0,1,3,0,2,0,0,220,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:03.00,Default,,0,0,0,,中文字体测试
EOF

ffmpeg -y -f lavfi -i color=c=black:s=720x1280:d=3 \
  -vf "ass=/tmp/font_test.ass:fontsdir=/fonts" \
  -frames:v 1 /tmp/font_test.jpg 2>&1 | grep -E "Loading font file|fontselect|Noto|Glyph|Added subtitle|Using font"
ls -lah /tmp/font_test.jpg
'
```

成功时应看到类似：

```text
Loading font file '/fonts/NotoSansCJK-Regular.ttc'
fontselect: (Noto Sans CJK SC, 400, 0) -> NotoSansCJKsc-Regular
```

如果仍看到：

```text
fontselect: failed to find any fallback ... for font: (Noto Sans CJK SC, 400, 0)
```

说明容器没有正确读取 `/fonts/NotoSansCJK-Regular.ttc`，优先检查 `.env` 的 `DATA_PATH`、compose 挂载路径、文件权限和容器是否已重启。

5. 重新跑任务或重新触发字幕烧录。

旧视频是在字体缺失时烧录出来的，字体修复后不会自动恢复字幕，必须重新执行 `video_subtitle_burn_v2` 或重新跑完整工作流。

6. 排查线上任务时看 ffmpeg-service 日志：

```bash
docker logs tools-ffmpeg-service --since=30m 2>&1 \
  | grep -E 'burn-subtitle|Loading font file|fontselect|Noto|Glyph|Added subtitle file|job done|job failed'
```

正常日志应包含：

```text
Added subtitle file: '...' (... events)
Loading font file '/fonts/NotoSansCJK-Regular.ttc'
fontselect: (Noto Sans CJK SC, 400, 0) -> NotoSansCJKsc-Regular
job done ... operation=burn-subtitle status=done
```

`subtitle:0kB` 不是错误。硬字幕已经烧进视频像素，不会保留独立 subtitle stream。

7. 代码侧已完成对应修改：
   - `burn_subtitle.go`：ffmpeg ass 滤镜加了 `fontsdir=/fonts` 参数
   - `executor.go`：`runFFmpeg` 成功时也会记录 stderr，方便排查字体等问题
   - `video_subtitle_burn_v2.go`：Linux 下 ASS Fontname 为 `Noto Sans CJK SC`，与宿主机 TTC 文件内部字体名一致

### 抽关键帧输出空图或 0 字节 JPG

**现象：** `extract-frame` 任务返回的 jpg 路径存在，但图片打不开或大小为 0 字节。ai-engine 中可能表现为 `ExtractKeyframesTool` 某些 keyframe 的 `local_path` 指向空图片，后续 VLM 分析失败或跳过。

**典型 ffmpeg 日志：**

```text
operation=extract-frame status=failed
[mjpeg] Non full-range YUV is non-standard
ff_frame_thread_encoder_init failed
Error while opening encoder
Nothing was written into output file
```

这个问题常见于 FFmpeg 8.x 对 `yuv420p(tv, ...)` 视频抽 JPEG 帧时，mjpeg 编码器初始化失败。

**代码侧修复：**

- `extract_frame.go` 对 `jpg/jpeg` 输出显式增加：
  - `-pix_fmt yuvj420p`
  - `-threads 1`
- `extract_frame.go` 在返回成功前校验输出文件存在且大小大于 0，避免空图继续流入 ai-engine。

**线上验证：**

```bash
docker logs tools-ffmpeg-service --since=30m 2>&1 \
  | grep -E 'extract-frame|ffmpeg completed|job done|job failed|mjpeg|Nothing was written|empty output'
```

正常日志应看到：

```text
job done ... operation=extract-frame status=done output_path=...jpg
```

如果仍失败，先检查输出文件大小：

```bash
docker exec tools-ffmpeg-service sh -lc 'ls -lah /tmp/media/output/*.jpg 2>/dev/null | tail -20'
```

或者按具体路径检查：

```bash
docker exec tools-ffmpeg-service sh -lc 'stat /tmp/media/output/xxx.jpg'
```
