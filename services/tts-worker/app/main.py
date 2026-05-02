import logging
import os
import time
from pathlib import Path

import edge_tts
from fastapi import FastAPI
from fastapi.responses import JSONResponse
from mutagen.mp3 import MP3
from pydantic import BaseModel, field_validator

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
)
logger = logging.getLogger(__name__)

STORAGE_LOCAL_DIR = os.environ.get("STORAGE_LOCAL_DIR", "/data/tts/audio")

app = FastAPI(title="tts-worker")


class SynthesizeRequest(BaseModel):
    task_id: str
    text: str
    voice: str = "zh-CN-XiaoxiaoNeural"
    rate: str = "+0%"
    volume: str = "+0%"
    pitch: str = "+0Hz"
    format: str = "mp3"

    @field_validator("text")
    @classmethod
    def text_not_empty(cls, v: str) -> str:
        if not v.strip():
            raise ValueError("text must not be empty")
        return v

    @field_validator("task_id")
    @classmethod
    def task_id_not_empty(cls, v: str) -> str:
        if not v.strip():
            raise ValueError("task_id must not be empty")
        return v

    @field_validator("format")
    @classmethod
    def format_mp3_only(cls, v: str) -> str:
        if v.lower() != "mp3":
            raise ValueError("only mp3 format is supported in MVP")
        return v.lower()


@app.post("/api/v1/synthesize")
async def synthesize(req: SynthesizeRequest):
    start = time.monotonic()
    output_dir = Path(STORAGE_LOCAL_DIR)
    output_dir.mkdir(parents=True, exist_ok=True)
    output_path = str(output_dir / f"{req.task_id}.{req.format}")

    try:
        communicate = edge_tts.Communicate(
            req.text,
            req.voice,
            rate=req.rate,
            volume=req.volume,
            pitch=req.pitch,
        )
        await communicate.save(output_path)
    except Exception as exc:
        latency_ms = int((time.monotonic() - start) * 1000)
        logger.error(
            "edge-tts failed task_id=%s voice=%s chars=%d latency_ms=%d error=%s",
            req.task_id,
            req.voice,
            len(req.text),
            latency_ms,
            exc,
        )
        return JSONResponse(
            status_code=500,
            content={
                "error_code": "edge_tts_failed",
                "error_message": str(exc),
            },
        )

    try:
        duration_sec = MP3(output_path).info.length
    except Exception as exc:
        logger.warning("duration probe failed task_id=%s: %s", req.task_id, exc)
        duration_sec = 0.0

    latency_ms = int((time.monotonic() - start) * 1000)
    logger.info(
        "synthesize ok task_id=%s voice=%s chars=%d output_path=%s latency_ms=%d duration_sec=%.2f",
        req.task_id,
        req.voice,
        len(req.text),
        output_path,
        latency_ms,
        duration_sec,
    )

    return {
        "task_id": req.task_id,
        "audio_local_path": output_path,
        "url": "",
        "duration_sec": duration_sec,
    }


@app.get("/healthz")
async def healthz():
    return {"status": "ok"}
