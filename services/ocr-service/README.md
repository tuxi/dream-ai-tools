# ocr-service

HTTP wrapper around PaddleOCR for keyframe text recognition.

## Run locally

```bash
cd services/ocr-service
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
cp config.example.yaml config.yaml
python -m ocr_service --config ./config.yaml
```

## APIs

```bash
curl http://127.0.0.1:8091/healthz
```

Single keyframe, synchronous:

```bash
curl -X POST http://127.0.0.1:8091/v1/ocr/keyframe \
  -H "Content-Type: application/json" \
  -d '{
    "task_id":"local-test",
    "language":"zh-CN",
    "keyframe":{
      "id":"kf_001",
      "segment_id":"seg_001",
      "time":0.25,
      "local_path":"/tmp/media/output/test.jpg"
    }
  }'
```

Batch keyframes, asynchronous:

```bash
curl -X POST http://127.0.0.1:8091/v1/ocr/keyframes/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "task_id":"local-test",
    "language":"zh-CN",
    "keyframes":[
      {
        "id":"kf_001",
        "segment_id":"seg_001",
        "time":0.25,
        "local_path":"/tmp/media/output/test.jpg"
      }
    ]
  }'

curl "http://127.0.0.1:8091/v1/ocr/keyframes/jobs/result?id=<job_id>"
```

Batch keyframes, synchronous compatibility endpoint:

```bash
curl -X POST http://127.0.0.1:8091/v1/ocr/keyframes \
  -H "Content-Type: application/json" \
  -d '{
    "task_id":"local-test",
    "language":"zh-CN",
    "keyframes":[
      {
        "id":"kf_001",
        "segment_id":"seg_001",
        "time":0.25,
        "local_path":"/tmp/media/output/test.jpg"
      }
    ]
  }'
```

Only files under `storage.allowed_roots` can be read.

## Model cache

Docker uses `/models/paddlex` by default. Pre-download models with:

```bash
python scripts/download_models.py --target ocr --config ./config.yaml
```

Set `ocr.offline: true` only after models are already present in the configured cache.
