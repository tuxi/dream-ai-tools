# dream-ai-tools 部署文档

适用于 Ubuntu 云服务器，与主项目 `dream-ai` 平行部署。

## 1. 服务概览

| 服务 | 语言 | 端口 | 说明 |
|------|------|------|------|
| ffmpeg-service | Go | 8089 | 异步 ffmpeg 操作 HTTP API，含 ffprobe |
| tts-service | Go | 8088 | 异步 TTS 任务 HTTP API |
| tts-worker | Python | 8090（内部） | 实际运行 edge-tts，仅 tts-service 调用 |
| redis | - | 内部 | ffmpeg-service 用 db=1，tts-service 用 db=0 |

**主项目访问方式：**
- `http://127.0.0.1:8088` → tts-service
- `http://127.0.0.1:8089` → ffmpeg-service

**路径一致性原则：** tts-worker 保存的音频路径、ffmpeg-service 的工作目录，都使用宿主机绝对路径，容器内通过 bind mount 挂载相同路径。主项目在宿主机上可直接读取这些路径的文件。

## 2. GitHub Actions 镜像构建

工作流文件：`/.github/workflows/docker-publish.yml`

推送到 `main` 分支或打 tag 时自动构建并推送 3 个镜像到 GHCR：

```
ghcr.io/tuxi/dream-ai-tools/ffmpeg-service:latest
ghcr.io/tuxi/dream-ai-tools/tts-service:latest
ghcr.io/tuxi/dream-ai-tools/tts-worker:latest
```

首次确认：
1. GitHub Actions 页面构建成功
2. 3 个 GHCR 包已生成
3. 如果包是 private，服务器需用 GitHub PAT 登录

服务器登录 GHCR：
```bash
echo YOUR_GITHUB_PAT | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

### 可选：同步推送阿里云 ACR

在 GitHub 仓库 Settings 中添加：

Variables：
- `ACR_REGISTRY`（如 `crpi-xxxx.cn-beijing.personal.cr.aliyuncs.com`）
- `ACR_NAMESPACE`（如 `dreamlog`）

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
  data/
    redis/                # Redis 持久化数据
    tts/
      audio/              # TTS 音频文件（主项目也会读取此目录）
    media/                # ffmpeg 工作目录（主项目也会读取此目录）
```

创建目录：

```bash
mkdir -p ~/apps/dream-ai-tools/config
mkdir -p ~/apps/dream-ai-tools/data/redis
mkdir -p ~/apps/dream-ai-tools/data/tts/audio
mkdir -p ~/apps/dream-ai-tools/data/media
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

DATA_PATH=/home/ubuntu/apps/dream-ai-tools/data
CONFIG_PATH=/home/ubuntu/apps/dream-ai-tools/config
REDIS_DATA_PATH=/home/ubuntu/apps/dream-ai-tools/data/redis

REDIS_PASSWORD=change-this-redis-password

TTS_SERVICE_PORT=8088
FFMPEG_SERVICE_PORT=8089
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

## 7. 启动服务

```bash
cd ~/apps/dream-ai-tools

# 拉取最新镜像
docker compose -f compose.yml --env-file .env pull

# 启动所有服务
docker compose -f compose.yml --env-file .env up -d

# 查看状态
docker compose -f compose.yml --env-file .env ps
```

期望状态：
- `tools-redis` → healthy
- `tools-tts-worker` → healthy（start_period 15s，稍等片刻）
- `tools-tts-service` → healthy
- `tools-ffmpeg-service` → healthy

## 8. 验证服务

```bash
# tts-service
curl http://127.0.0.1:8088/healthz

# ffmpeg-service
curl http://127.0.0.1:8089/healthz
```

两个接口都应返回 `{"status":"ok"}`。

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
