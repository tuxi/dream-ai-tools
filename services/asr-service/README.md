# asr-service

HTTP wrapper around FunASR. It accepts a shared local audio or video path, extracts 16 kHz mono WAV from video inputs, and returns transcript segments.

## Run locally

```bash
cd services/asr-service
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
cp config.example.yaml config.yaml
python -m asr_service --config ./config.yaml
```

## APIs

```bash
curl http://127.0.0.1:8092/healthz
```

```bash
curl -X POST http://127.0.0.1:8092/v1/asr/transcribe \
  -H "Content-Type: application/json" \
  -d '{
    "task_id":"local-test",
    "language":"zh-CN",
    "local_video_path":"/tmp/media/input/test.mp4",
    "with_timestamps":true
  }'
```

Only files under `storage.allowed_roots` can be read.

## Model cache

Docker uses `/models/modelscope` by default. Pre-download models with:

```bash
python scripts/download_models.py --target asr --config ./config.yaml
```

Set `asr.offline: true` only after models are already present in the configured cache.
