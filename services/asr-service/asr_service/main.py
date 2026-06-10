import asyncio
import json
import logging
import os
import re
import shutil
import subprocess
import threading
import time
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

import yaml
from fastapi import FastAPI
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from pydantic import BaseModel, field_validator, model_validator

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("asr-service")


DEFAULT_CONFIG: Dict[str, Any] = {
    "server": {"host": "0.0.0.0", "port": 8092},
    "asr": {
        "engine": "funasr",
        "language": "zh-CN",
        "use_gpu": False,
        "model": "auto",
        "model_cache_dir": "",
        "offline": False,
        "sample_rate": 16000,
        "max_duration_seconds": 120,
        "enable_timestamps": True,
        "max_concurrency": 1,
        "max_file_size_mb": 500,
        "work_dir": "/tmp/dream-ai-tools/asr",
        "ffmpeg_path": "ffmpeg",
        "ffprobe_path": "ffprobe",
    },
    "storage": {"allowed_roots": ["/tmp", "/data/media"]},
}


def deep_merge(base: Dict[str, Any], override: Dict[str, Any]) -> Dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(merged.get(key), dict):
            merged[key] = deep_merge(merged[key], value)
        else:
            merged[key] = value
    return merged


def load_config() -> Dict[str, Any]:
    path = os.environ.get("CONFIG_PATH", "config.yaml")
    if not path or not Path(path).exists():
        return DEFAULT_CONFIG
    with open(path, "r", encoding="utf-8") as f:
        data = yaml.safe_load(f) or {}
    return deep_merge(DEFAULT_CONFIG, data)


CONFIG = load_config()


def setup_asr_model_cache(config: Dict[str, Any]) -> None:
    cache_dir = str(config["asr"].get("model_cache_dir") or "").strip()
    if cache_dir:
        if cache_dir.startswith("/models"):
            default_root = "/models"
        else:
            default_root = str(Path(cache_dir).resolve().parent)

        os.environ.setdefault("XDG_CACHE_HOME", str(Path(default_root) / "cache"))
        os.environ.setdefault("HF_HOME", str(Path(default_root) / "huggingface"))
        os.environ.setdefault("HF_HUB_CACHE", str(Path(default_root) / "huggingface/hub"))
        os.environ.setdefault("MODELSCOPE_CACHE", cache_dir)
        os.environ.setdefault("TORCH_HOME", str(Path(default_root) / "torch"))

    if bool(config["asr"].get("offline", False)):
        cache_dir = cache_dir or os.environ.get("MODELSCOPE_CACHE", "")
        if not cache_dir:
            raise RuntimeError("asr.offline=true requires asr.model_cache_dir or MODELSCOPE_CACHE")
        os.environ.setdefault("HF_HUB_OFFLINE", "1")
        os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
        os.environ.setdefault("MODELSCOPE_OFFLINE", "1")
        ensure_asr_model_cache_exists(cache_dir)

    logger.info(
        "asr model cache configured cache_dir=%s hf_home=%s torch_home=%s offline=%s",
        cache_dir or os.environ.get("MODELSCOPE_CACHE", "default"),
        os.environ.get("HF_HOME", ""),
        os.environ.get("TORCH_HOME", ""),
        bool(config["asr"].get("offline", False)),
    )


def ensure_asr_model_cache_exists(cache_dir: str) -> None:
    root = Path(cache_dir)
    if not root.exists() or not any(path.is_file() for path in root.rglob("*")):
        raise RuntimeError(f"model files not found under {cache_dir}, please run model download first")


setup_asr_model_cache(CONFIG)


class TranscribeRequest(BaseModel):
    task_id: str = ""
    language: str = "zh-CN"
    local_video_path: str = ""
    local_audio_path: str = ""
    with_timestamps: bool = True
    model: str = "auto"

    @field_validator("task_id")
    @classmethod
    def task_id_trim(cls, value: str) -> str:
        return (value or "").strip()

    @model_validator(mode="after")
    def one_input_required(self) -> "TranscribeRequest":
        if not self.local_video_path.strip() and not self.local_audio_path.strip():
            raise ValueError("local_video_path or local_audio_path is required")
        return self


class CapacityLimiter:
    def __init__(self, limit: int) -> None:
        self.limit = max(1, int(limit))
        self.in_flight = 0
        self.lock = asyncio.Lock()

    async def acquire(self) -> bool:
        async with self.lock:
            if self.in_flight >= self.limit:
                return False
            self.in_flight += 1
            return True

    async def release(self) -> None:
        async with self.lock:
            self.in_flight = max(0, self.in_flight - 1)


def path_in_root(path: Path, root: Path) -> bool:
    try:
        path.relative_to(root)
        return True
    except ValueError:
        return False


def resolve_allowed_file(raw_path: str) -> Tuple[Optional[Path], Optional[str]]:
    path = Path(raw_path)
    if not path.is_absolute():
        return None, "path must be absolute"

    resolved = path.resolve(strict=False)
    allowed_roots = [Path(p).resolve(strict=False) for p in CONFIG["storage"]["allowed_roots"]]
    if not any(path_in_root(resolved, root) for root in allowed_roots):
        return None, "path is outside allowed_roots"
    if not resolved.exists():
        return None, "file does not exist"
    if not resolved.is_file():
        return None, "path is not a file"

    max_bytes = int(CONFIG["asr"].get("max_file_size_mb", 500)) * 1024 * 1024
    if max_bytes > 0 and resolved.stat().st_size > max_bytes:
        return None, "file exceeds max_file_size_mb"
    return resolved, None


def safe_task_id(task_id: str) -> str:
    value = re.sub(r"[^A-Za-z0-9_.-]+", "_", task_id or "local")
    return value[:80] or "local"


def seconds(value: Any) -> float:
    number = float(value or 0)
    if number > 1000:
        return number / 1000.0
    return number


def to_loggable(value: Any) -> Any:
    if hasattr(value, "tolist"):
        return value.tolist()
    if hasattr(value, "item"):
        try:
            return value.item()
        except Exception:
            pass
    if isinstance(value, dict):
        return {str(key): to_loggable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [to_loggable(item) for item in value]
    return value


def normalize_time_value(value: Any, duration: Optional[float]) -> float:
    try:
        number = float(value or 0)
    except Exception:
        return 0.0

    if duration and number > duration + 1:
        millis = number / 1000.0
        if 0 <= millis <= duration + 1:
            return millis
        centis = number / 100.0
        if 0 <= centis <= duration + 1:
            return centis
    elif not duration and number > 1000:
        return number / 1000.0
    return number


def clamp_time(value: float, duration: Optional[float]) -> float:
    if value < 0:
        return 0.0
    if duration and duration > 0 and value > duration:
        return float(duration)
    return value


def normalize_segment_times(start: Any, end: Any, duration: Optional[float]) -> Tuple[float, float]:
    start_sec = clamp_time(normalize_time_value(start, duration), duration)
    end_sec = clamp_time(normalize_time_value(end, duration), duration)

    if duration and duration > 0:
        if start_sec >= duration or end_sec <= 0 or start_sec >= end_sec:
            start_sec = 0.0
            end_sec = float(duration)
    elif start_sec >= end_sec:
        start_sec = 0.0
        end_sec = max(float(end_sec), 0.01)

    if start_sec >= end_sec:
        end_sec = min(float(duration), start_sec + 0.01) if duration and duration > 0 else start_sec + 0.01
        if start_sec >= end_sec:
            start_sec = max(0.0, end_sec - 0.01)

    return round(start_sec, 3), round(end_sec, 3)


def split_chinese_sentences(text: str) -> List[str]:
    chunks = re.findall(r"[^。！？!?，,；;]+[。！？!?，,；;]?", text)
    sentences = [chunk.strip() for chunk in chunks if chunk.strip()]
    return sentences or [text.strip()]


def split_segment_by_text(segment: Dict[str, Any]) -> List[Dict[str, Any]]:
    text = str(segment.get("text") or "").strip()
    start = float(segment.get("start") or 0)
    end = float(segment.get("end") or 0)
    if not text or end <= start:
        return [segment]

    sentences = split_chinese_sentences(text)
    if len(sentences) <= 1:
        return [segment]

    total_chars = sum(len(sentence) for sentence in sentences)
    if total_chars <= 0:
        return [segment]

    cursor = start
    duration = end - start
    split_items: List[Dict[str, Any]] = []
    for idx, sentence in enumerate(sentences):
        if idx == len(sentences) - 1:
            sentence_end = end
        else:
            sentence_end = cursor + duration * (len(sentence) / total_chars)
        split_items.append(
            {
                "start": round(cursor, 3),
                "end": round(max(sentence_end, cursor + 0.01), 3),
                "text": sentence,
                "speaker": str(segment.get("speaker") or ""),
                "confidence": float(segment.get("confidence") or 0),
                "source": "asr",
            }
        )
        cursor = sentence_end
    return split_items


def normalize_transcript(segments: List[Dict[str, Any]], duration: Optional[float]) -> List[Dict[str, Any]]:
    normalized: List[Dict[str, Any]] = []
    for segment in segments:
        text = str(segment.get("text") or "").strip()
        if not text:
            continue
        start, end = normalize_segment_times(segment.get("start"), segment.get("end"), duration)
        normalized.append(
            {
                "start": start,
                "end": end,
                "text": text,
                "speaker": str(segment.get("speaker") or ""),
                "confidence": float(segment.get("confidence") or 0),
                "source": "asr",
            }
        )

    if len(normalized) == 1:
        return split_segment_by_text(normalized[0])
    return [segment for segment in normalized if float(segment["start"]) < float(segment["end"])]


def strip_funasr_tags(text: str) -> str:
    return re.sub(r"<\|[^>]+?\|>", "", text or "").strip()


def run_command(args: List[str]) -> subprocess.CompletedProcess:
    return subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=False)


def probe_duration(path: Path) -> Optional[float]:
    ffprobe = str(CONFIG["asr"].get("ffprobe_path", "ffprobe"))
    if not shutil.which(ffprobe) and "/" not in ffprobe:
        return None
    result = run_command([ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "json", str(path)])
    if result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout or "{}")
        return float(data.get("format", {}).get("duration") or 0)
    except Exception:
        return None


def extract_audio(video_path: Path, task_id: str) -> Tuple[Optional[Path], Optional[Dict[str, str]]]:
    ffmpeg = str(CONFIG["asr"].get("ffmpeg_path", "ffmpeg"))
    if not shutil.which(ffmpeg) and "/" not in ffmpeg:
        return None, {"type": "asr_failed", "message": "ffmpeg not found"}

    output_dir = Path(CONFIG["asr"].get("work_dir", "/tmp/dream-ai-tools/asr")) / safe_task_id(task_id)
    output_dir.mkdir(parents=True, exist_ok=True)
    audio_path = output_dir / "audio.wav"
    sample_rate = str(int(CONFIG["asr"].get("sample_rate", 16000)))
    result = run_command([ffmpeg, "-y", "-i", str(video_path), "-vn", "-ac", "1", "-ar", sample_rate, str(audio_path)])
    if result.returncode != 0 or not audio_path.exists() or audio_path.stat().st_size == 0:
        message = (result.stderr or result.stdout or "audio extraction failed").strip()[-500:]
        lower = message.lower()
        warning_type = "no_audio" if "audio" in lower and ("stream" in lower or "does not contain" in lower) else "asr_failed"
        return None, {"type": warning_type, "message": message or "input has no audio"}
    return audio_path, None


class FunASREngine:
    def __init__(self) -> None:
        self._model = None
        self._model_name = ""
        self._lock = threading.Lock()

    def transcribe(self, audio_path: Path, req: TranscribeRequest, duration: Optional[float]) -> List[Dict[str, Any]]:
        model_name = self._resolve_model_name(req.model)
        self.ensure_loaded(model_name)
        return self._transcribe_sync(audio_path, req.with_timestamps, duration)

    def ensure_loaded(self, model_name: str = "auto") -> None:
        model_name = self._resolve_model_name(model_name)
        if self._model is None or self._model_name != model_name:
            with self._lock:
                if self._model is None or self._model_name != model_name:
                    self._model = self._create_model(model_name)
                    self._model_name = model_name

    @property
    def model_loaded(self) -> bool:
        return self._model is not None

    def _resolve_model_name(self, request_model: str) -> str:
        configured = str(CONFIG["asr"].get("model", "auto"))
        value = request_model if request_model and request_model != "auto" else configured
        if not value or value == "auto" or value == "funasr":
            return "paraformer-zh"
        if value == "sensevoice":
            return "iic/SenseVoiceSmall"
        return value

    def _create_model(self, model_name: str) -> Any:
        from funasr import AutoModel

        kwargs: Dict[str, Any] = {"model": model_name, "disable_update": True}
        if model_name != "iic/SenseVoiceSmall":
            kwargs.update({"vad_model": "fsmn-vad", "punc_model": "ct-punc"})
        logger.info("loading FunASR model=%s", model_name)
        try:
            return AutoModel(**kwargs)
        except TypeError:
            kwargs.pop("disable_update", None)
            return AutoModel(**kwargs)

    def _transcribe_sync(self, audio_path: Path, with_timestamps: bool, duration: Optional[float]) -> List[Dict[str, Any]]:
        kwargs: Dict[str, Any] = {"input": str(audio_path), "batch_size_s": 300}
        if with_timestamps:
            kwargs["return_timestamp"] = True
        try:
            result = self._model.generate(**kwargs)
        except TypeError:
            kwargs.pop("return_timestamp", None)
            result = self._model.generate(**kwargs)
        try:
            logger.info("funasr raw result=%s", json.dumps(to_loggable(result), ensure_ascii=False))
        except Exception:
            logger.info("funasr raw result repr=%r", result)
        return self._parse_result(result, duration)

    def _parse_result(self, result: Any, duration: Optional[float]) -> List[Dict[str, Any]]:
        transcript: List[Dict[str, Any]] = []
        records = result if isinstance(result, list) else [result]
        for record in records:
            if not isinstance(record, dict):
                continue
            sentence_info = record.get("sentence_info") or record.get("sentences") or []
            record_added = False
            for sentence in sentence_info:
                text = strip_funasr_tags(str(sentence.get("text") or ""))
                if not text:
                    continue
                record_added = True
                transcript.append(
                    {
                        "start": seconds(sentence.get("start")),
                        "end": seconds(sentence.get("end")),
                        "text": text,
                        "speaker": str(sentence.get("speaker") or ""),
                        "confidence": float(sentence.get("confidence") or 0),
                        "source": "asr",
                    }
                )
            if record_added:
                continue

            text = strip_funasr_tags(str(record.get("text") or ""))
            if not text:
                continue
            timestamp = record.get("timestamp") or []
            start = 0.0
            end = float(duration or 0)
            if timestamp:
                try:
                    start = seconds(timestamp[0][0])
                    end = seconds(timestamp[-1][-1])
                except Exception:
                    pass
            transcript.append(
                {
                    "start": start,
                    "end": end,
                    "text": text,
                    "speaker": "",
                    "confidence": float(record.get("confidence") or 0),
                    "source": "asr",
                }
            )
        return normalize_transcript(transcript, duration)


engine = FunASREngine()
if bool(CONFIG["asr"].get("offline", False)):
    engine.ensure_loaded(str(CONFIG["asr"].get("model", "auto")))
limiter = CapacityLimiter(CONFIG["asr"].get("max_concurrency", 1))
app = FastAPI(title="asr-service")


@app.exception_handler(RequestValidationError)
async def validation_exception_handler(_, exc: RequestValidationError):
    return JSONResponse(status_code=400, content={"error": "invalid_request", "message": str(exc)})


@app.get("/healthz")
async def healthz() -> Dict[str, Any]:
    return {
        "status": "ok",
        "service": "asr-service",
        "engine": str(CONFIG["asr"]["engine"]),
        "model_loaded": engine.model_loaded,
        "cache_dir": str(CONFIG["asr"].get("model_cache_dir") or os.environ.get("MODELSCOPE_CACHE", "default")),
        "offline": bool(CONFIG["asr"].get("offline", False)),
    }


@app.get("/readyz")
async def readyz():
    content = await healthz()
    if not engine.model_loaded:
        content["status"] = "loading"
        return JSONResponse(status_code=503, content=content)
    content["status"] = "ready"
    return content


@app.post("/v1/asr/transcribe")
async def transcribe(req: TranscribeRequest):
    if not await limiter.acquire():
        return JSONResponse(status_code=429, content={"error": "too_many_requests", "message": "max_concurrency exceeded"})

    started = time.monotonic()
    try:
        return await asyncio.to_thread(process_transcribe_request, req)
    finally:
        await limiter.release()
        logger.info(
            "asr request finished task_id=%s latency_ms=%d",
            req.task_id,
            int((time.monotonic() - started) * 1000),
        )


def process_transcribe_request(req: TranscribeRequest):
    input_path_raw = req.local_audio_path.strip() or req.local_video_path.strip()
    input_path, error = resolve_allowed_file(input_path_raw)
    if error:
        return JSONResponse(status_code=400, content={"error": "invalid_input_path", "message": error})

    duration = probe_duration(input_path)
    max_duration = float(CONFIG["asr"].get("max_duration_seconds", 120))
    if duration is not None and max_duration > 0 and duration > max_duration:
        return JSONResponse(status_code=400, content={"error": "duration_too_long", "message": "max_duration_seconds exceeded"})

    warnings: List[Dict[str, str]] = []
    audio_path = input_path
    if not req.local_audio_path.strip():
        audio_path, warning = extract_audio(input_path, req.task_id)
        if warning:
            return {"transcript": [], "warnings": [warning]}
        duration = probe_duration(audio_path) or duration

    try:
        transcript = engine.transcribe(audio_path, req, duration)
    except Exception as exc:
        logger.exception("asr failed task_id=%s path=%s", req.task_id, audio_path)
        warnings.append({"type": "asr_failed", "message": str(exc)})
        return {"transcript": [], "warnings": warnings}

    transcript = [item for item in transcript if str(item.get("text") or "").strip()]
    if not transcript:
        warnings.append({"type": "asr_empty", "message": "model returned empty transcript"})
    return {"transcript": transcript, "warnings": warnings}
