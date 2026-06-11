FROM python:3.11-slim

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        wget \
        libglib2.0-0 \
        libgl1 \
        libgomp1 \
        libsm6 \
        libxext6 \
        tesseract-ocr \
        tesseract-ocr-chi-sim \
        tesseract-ocr-chi-tra \
        tesseract-ocr-eng \
        tesseract-ocr-jpn \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY ocr_service/ ./ocr_service/
COPY scripts/ ./scripts/
COPY config.example.yaml ./config.yaml

ENV CONFIG_PATH=/app/config.yaml

EXPOSE 8091

CMD ["python", "-m", "ocr_service", "--config", "/app/config.yaml"]
