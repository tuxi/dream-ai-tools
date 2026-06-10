FROM python:3.11-slim

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends wget ffmpeg libsndfile1 libgomp1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY asr_service/ ./asr_service/
COPY scripts/ ./scripts/
COPY config.example.yaml ./config.yaml

ENV CONFIG_PATH=/app/config.yaml

EXPOSE 8092

CMD ["python", "-m", "asr_service", "--config", "/app/config.yaml"]
