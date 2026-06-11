# dream-ai-tools

HTTP service wrappers for media and model tools used by dream-ai workflows.

| Service | Port | Purpose |
|---------|------|---------|
| `services/tts-service` | 8088 | TTS task API and worker orchestration |
| `services/tts-worker` | 8090 | edge-tts execution worker |
| `services/ffmpeg-service` | 8089 | ffmpeg / ffprobe media operations |
| `services/ocr-service` | 8091 | PaddleOCR keyframe text recognition |
| `services/asr-service` | 8092 | FunASR audio/video transcription |

Local service entrypoints:

```bash
python -m ocr_service --config ./config.yaml
python -m asr_service --config ./config.yaml
```

Docker Compose entrypoint:

```bash
cd services
docker compose up ocr-service
docker compose --profile asr up asr-service
```

Model caches for OCR / ASR should be persistent. Docker Compose mounts `${DATA_PATH}/models` to `/models`; local Mac development can use:

```bash
export XDG_CACHE_HOME=/Users/xiaoyuan/.cache/dream-ai/cache
export PADDLE_HOME=/Users/xiaoyuan/.cache/dream-ai/paddle
export PADDLEX_HOME=/Users/xiaoyuan/.cache/dream-ai/paddlex
export PADDLEOCR_HOME=/Users/xiaoyuan/.cache/dream-ai/paddleocr
export HF_HOME=/Users/xiaoyuan/.cache/dream-ai/huggingface
export HF_HUB_CACHE=/Users/xiaoyuan/.cache/dream-ai/huggingface/hub
export MODELSCOPE_CACHE=/Users/xiaoyuan/.cache/dream-ai/modelscope
export TORCH_HOME=/Users/xiaoyuan/.cache/dream-ai/torch
```
