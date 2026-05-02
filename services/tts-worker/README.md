# tts-worker

Python FastAPI service that wraps `edge-tts` and exposes a single HTTP endpoint for audio synthesis.

## API

### POST /api/v1/synthesize

Request:
```json
{
  "task_id": "tts_abc123",
  "text": "你好世界",
  "voice": "zh-CN-XiaoxiaoNeural",
  "rate": "+0%",
  "volume": "+0%",
  "pitch": "+0Hz",
  "format": "mp3"
}
```

Success response (200):
```json
{
  "task_id": "tts_abc123",
  "audio_local_path": "/data/tts/audio/tts_abc123.mp3",
  "url": "",
  "duration_sec": 1.82
}
```

Error response (500):
```json
{
  "error_code": "edge_tts_failed",
  "error_message": "..."
}
```

### GET /healthz

```json
{"status": "ok"}
```

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `STORAGE_LOCAL_DIR` | `/data/tts/audio` | Directory to write mp3 files |
| `PORT` | `8090` | Uvicorn listen port (passed via CLI) |

## Local development

```bash
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
STORAGE_LOCAL_DIR=/tmp/tts-audio uvicorn app.main:app --host 0.0.0.0 --port 8090 --reload
```

## Notes

- Only `format=mp3` is supported in MVP.
- The worker does **not** generate `task_id`; the caller (tts-service) provides it.
- The worker is not exposed to the public internet; only tts-service should call it.
