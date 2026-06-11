import asyncio
import json
import logging
import os
import threading
import time
import uuid
from collections.abc import Mapping
from dataclasses import dataclass, field
from numbers import Number
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

import yaml
from fastapi import FastAPI
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field, field_validator

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("ocr-service")


DEFAULT_CONFIG: Dict[str, Any] = {
    "server": {"host": "0.0.0.0", "port": 8091},
    "ocr": {
        "engine": "paddleocr",
        "lang": "ch",
        "use_gpu": False,
        "ocr_version": "PP-OCRv5",
        "use_doc_orientation_classify": False,
        "use_doc_unwarping": False,
        "use_textline_orientation": False,
        "enable_mkldnn": False,
        "enable_hpi": False,
        "cpu_threads": 2,
        "model_cache_dir": "",
        "offline": False,
        "min_confidence": 0.6,
        "max_images_per_request": 120,
        "dedupe": True,
        "dedupe_window_seconds": 2.0,
        "max_concurrency": 2,
        "max_file_size_mb": 50,
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


def setup_ocr_model_cache(config: Dict[str, Any]) -> None:
    os.environ.setdefault("FLAGS_use_mkldnn", "0")
    os.environ.setdefault("FLAGS_enable_pir_api", "0")
    cache_dir = str(config["ocr"].get("model_cache_dir") or "").strip()
    if cache_dir:
        if cache_dir.startswith("/models"):
            default_root = "/models"
        else:
            default_root = str(Path(cache_dir).resolve().parent)
        os.environ.setdefault("XDG_CACHE_HOME", str(Path(default_root) / "cache"))
        os.environ.setdefault("PADDLE_HOME", str(Path(default_root) / "paddle"))
        os.environ.setdefault("PADDLEX_HOME", cache_dir)
        os.environ.setdefault("PADDLEOCR_HOME", str(Path(default_root) / "paddleocr"))
        setup_paddlex_home_link(cache_dir)

    if bool(config["ocr"].get("offline", False)):
        cache_dir = cache_dir or os.environ.get("PADDLEX_HOME", "")
        if not cache_dir:
            raise RuntimeError("ocr.offline=true requires ocr.model_cache_dir or PADDLEX_HOME")
        os.environ.setdefault("PADDLEX_OFFLINE", "1")
        os.environ.setdefault("PADDLEOCR_OFFLINE", "1")
        ensure_ocr_model_cache_exists(cache_dir, str(config["ocr"].get("ocr_version", "PP-OCRv5")))

    logger.info(
        "ocr model cache configured cache_dir=%s xdg_cache_home=%s offline=%s",
        cache_dir or os.environ.get("PADDLEX_HOME", "default"),
        os.environ.get("XDG_CACHE_HOME", ""),
        bool(config["ocr"].get("offline", False)),
    )


def setup_paddlex_home_link(cache_dir: str) -> None:
    target = Path(cache_dir)
    target.mkdir(parents=True, exist_ok=True)
    legacy = Path.home() / ".paddlex"
    try:
        if legacy.is_symlink() and legacy.resolve(strict=False) == target.resolve(strict=False):
            return
        if legacy.is_mount():
            logger.info("using mounted legacy paddlex cache path=%s target=%s", legacy, target)
            return
        if legacy.exists():
            if legacy.is_dir() and not any(legacy.iterdir()):
                legacy.rmdir()
            else:
                logger.warning("legacy paddlex cache exists path=%s target=%s", legacy, target)
                return
        legacy.symlink_to(target, target_is_directory=True)
        logger.info("linked legacy paddlex cache path=%s target=%s", legacy, target)
    except Exception as exc:
        logger.warning("failed to link legacy paddlex cache path=%s target=%s error=%s", legacy, target, exc)


def ensure_ocr_model_cache_exists(cache_dir: str, ocr_version: str) -> None:
    root = Path(cache_dir)
    if not root.exists():
        raise RuntimeError(f"model files not found under {cache_dir}, please run model download first")

    expected = []
    if ocr_version == "PP-OCRv5":
        expected = [
            "PP-LCNet_x1_0_doc_ori",
            "UVDoc",
            "PP-LCNet_x1_0_textline_ori",
            "PP-OCRv5_server_det",
            "PP-OCRv5_server_rec",
        ]
    official_models = root / "official_models"
    if expected:
        if not official_models.exists():
            raise RuntimeError(f"model files not found under {official_models}, please run model download first")
        missing = [
            name
            for name in expected
            if not (official_models / name).exists() or not any(path.is_file() for path in (official_models / name).rglob("*"))
        ]
        if missing:
            raise RuntimeError(
                "model files not found under %s, missing %s, please run model download first"
                % (official_models, ", ".join(missing))
            )
        return

    if not any(path.is_file() for path in root.rglob("*")):
        raise RuntimeError(f"model files not found under {cache_dir}, please run model download first")


setup_ocr_model_cache(CONFIG)


class Keyframe(BaseModel):
    id: str
    segment_id: str
    time: float = 0
    local_path: str

    @field_validator("id", "segment_id", "local_path")
    @classmethod
    def not_empty(cls, value: str) -> str:
        if not value or not value.strip():
            raise ValueError("must not be empty")
        return value.strip()


class OCRRequest(BaseModel):
    task_id: str = ""
    language: str = "zh-CN"
    min_confidence: Optional[float] = None
    dedupe: Optional[bool] = None
    keyframes: List[Keyframe] = Field(default_factory=list)


class SingleOCRRequest(BaseModel):
    task_id: str = ""
    language: str = "zh-CN"
    min_confidence: Optional[float] = None
    keyframe: Keyframe


@dataclass
class OCRJob:
    id: str
    task_id: str
    status: str = "processing"
    result: Optional[Dict[str, Any]] = None
    error_code: str = ""
    error_message: str = ""
    created_at: float = field(default_factory=time.time)
    updated_at: float = field(default_factory=time.time)


class OCRJobStore:
    def __init__(self) -> None:
        self._jobs: Dict[str, OCRJob] = {}
        self._lock = threading.RLock()

    def create(self, task_id: str) -> OCRJob:
        job = OCRJob(id="ocr_" + uuid.uuid4().hex, task_id=task_id)
        with self._lock:
            self._jobs[job.id] = job
        return job

    def get(self, job_id: str) -> Optional[OCRJob]:
        with self._lock:
            return self._jobs.get(job_id)

    def mark_done(self, job_id: str, result: Dict[str, Any]) -> None:
        with self._lock:
            job = self._jobs.get(job_id)
            if not job:
                return
            job.status = "done"
            job.result = result
            job.updated_at = time.time()

    def mark_failed(self, job_id: str, error_code: str, error_message: str) -> None:
        with self._lock:
            job = self._jobs.get(job_id)
            if not job:
                return
            job.status = "failed"
            job.error_code = error_code
            job.error_message = error_message
            job.updated_at = time.time()


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

    max_bytes = int(CONFIG["ocr"].get("max_file_size_mb", 50)) * 1024 * 1024
    if max_bytes > 0 and resolved.stat().st_size > max_bytes:
        return None, "file exceeds max_file_size_mb"
    return resolved, None


def normalize_bbox(raw: Any) -> List[int]:
    if raw is None:
        return []
    raw = to_plain(raw)
    try:
        if len(raw) == 4 and all(isinstance(v, (int, float)) for v in raw):
            x1, y1, x2, y2 = raw
            return [int(round(x1)), int(round(y1)), int(round(x2)), int(round(y2))]
        points = []
        for point in raw:
            if isinstance(point, (list, tuple)) and len(point) >= 2:
                points.append((float(point[0]), float(point[1])))
        if not points:
            return []
        xs = [p[0] for p in points]
        ys = [p[1] for p in points]
        return [
            int(round(min(xs))),
            int(round(min(ys))),
            int(round(max(xs))),
            int(round(max(ys))),
        ]
    except Exception:
        return []


def to_plain(value: Any) -> Any:
    if hasattr(value, "tolist"):
        return value.tolist()
    if hasattr(value, "item"):
        try:
            return value.item()
        except Exception:
            pass
    if isinstance(value, Mapping):
        return {key: to_plain(item) for key, item in value.items()}
    if isinstance(value, dict):
        return {key: to_plain(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [to_plain(item) for item in value]
    return value


def get_first(data: Dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in data and data[key] is not None:
            return to_plain(data[key])
    return []


class PaddleOCREngine:
    def __init__(self) -> None:
        self._ocr = None
        self._lock = threading.Lock()

    def recognize(self, image_path: Path, language: str) -> List[Dict[str, Any]]:
        self.ensure_loaded(language)
        return self._recognize_sync(image_path)

    def ensure_loaded(self, language: str = "zh-CN") -> None:
        if self._ocr is None:
            with self._lock:
                if self._ocr is None:
                    self._ocr = self._create_ocr(language)

    @property
    def model_loaded(self) -> bool:
        return self._ocr is not None

    def _create_ocr(self, language: str) -> Any:
        from paddleocr import PaddleOCR

        lang = CONFIG["ocr"].get("lang") or self._map_language(language)
        use_gpu = bool(CONFIG["ocr"].get("use_gpu", False))
        ocr_version = str(CONFIG["ocr"].get("ocr_version", "PP-OCRv5"))
        stable_kwargs = {
            "use_doc_orientation_classify": bool(CONFIG["ocr"].get("use_doc_orientation_classify", False)),
            "use_doc_unwarping": bool(CONFIG["ocr"].get("use_doc_unwarping", False)),
            "use_textline_orientation": bool(CONFIG["ocr"].get("use_textline_orientation", False)),
        }
        runtime_kwargs = {
            "enable_mkldnn": bool(CONFIG["ocr"].get("enable_mkldnn", False)),
            "enable_hpi": bool(CONFIG["ocr"].get("enable_hpi", False)),
            "cpu_threads": int(CONFIG["ocr"].get("cpu_threads", 2)),
        }
        candidates = [
            {
                "lang": lang,
                "ocr_version": ocr_version,
                **stable_kwargs,
                **runtime_kwargs,
            },
            {"lang": lang, **stable_kwargs, **runtime_kwargs},
            {
                "lang": lang,
                "use_gpu": use_gpu,
                "ocr_version": ocr_version,
                **stable_kwargs,
                **runtime_kwargs,
            },
            {"lang": lang, "use_gpu": use_gpu, **stable_kwargs, **runtime_kwargs},
            {
                "lang": lang,
                "ocr_version": ocr_version,
                **stable_kwargs,
            },
            {"lang": lang, **stable_kwargs},
            {"lang": lang, "ocr_version": ocr_version},
            {"lang": lang},
        ]
        last_error: Optional[Exception] = None
        for kwargs in candidates:
            try:
                logger.info("loading PaddleOCR kwargs=%s", kwargs)
                return PaddleOCR(**kwargs)
            except Exception as exc:
                last_error = exc
                message = str(exc).lower()
                if "unknown argument" in message or "unexpected keyword" in message or isinstance(exc, TypeError):
                    logger.info("retrying PaddleOCR without incompatible kwargs error=%s", exc)
                    continue
                raise
        raise RuntimeError("failed to initialize PaddleOCR: %s" % last_error)

    def _map_language(self, language: str) -> str:
        value = (language or "").lower()
        if value in ("zh-cn", "zh-hans", "zh", "ch"):
            return "ch"
        if value in ("zh-tw", "zh-hant", "chinese_cht"):
            return "chinese_cht"
        if value.startswith("en"):
            return "en"
        if value.startswith("ja"):
            return "japan"
        return "ch"

    def _recognize_sync(self, image_path: Path) -> List[Dict[str, Any]]:
        if hasattr(self._ocr, "predict"):
            try:
                result = self._ocr.predict(str(image_path))
                return self._parse_predict_result(result)
            except Exception as exc:
                logger.debug("PaddleOCR predict path failed: %s", exc)

        try:
            result = self._ocr.ocr(str(image_path), cls=True)
        except TypeError:
            result = self._ocr.ocr(str(image_path))
        return self._parse_ocr_result(result)

    def _parse_predict_result(self, result: Any) -> List[Dict[str, Any]]:
        items: List[Dict[str, Any]] = []
        pages = result if isinstance(result, list) else [result]
        for page in pages:
            data = self._page_to_dict(page)
            if not isinstance(data, dict):
                continue
            texts = get_first(data, "rec_texts", "texts")
            scores = get_first(data, "rec_scores", "scores")
            boxes = get_first(data, "rec_polys", "rec_boxes", "dt_polys", "boxes")
            for idx, text in enumerate(texts):
                score = scores[idx] if idx < len(scores) else 0
                box = boxes[idx] if idx < len(boxes) else None
                items.append({"text": str(text), "confidence": float(score or 0), "bbox": normalize_bbox(box)})
        return items

    def _page_to_dict(self, page: Any) -> Optional[Dict[str, Any]]:
        data = page.res if hasattr(page, "res") else page
        if isinstance(data, Mapping):
            return to_plain(dict(data))
        if hasattr(data, "items"):
            try:
                return to_plain(dict(data.items()))
            except Exception:
                pass
        if hasattr(data, "json"):
            data = data.json() if callable(data.json) else data.json
            if isinstance(data, str):
                data = json.loads(data)
        if hasattr(data, "to_dict"):
            data = data.to_dict()
        data = to_plain(data)
        return data if isinstance(data, dict) else None

    def _parse_ocr_result(self, result: Any) -> List[Dict[str, Any]]:
        items: List[Dict[str, Any]] = []
        result = to_plain(result)

        def visit(node: Any) -> None:
            if not isinstance(node, list):
                return
            if self._looks_like_ocr_line(node):
                box = node[0]
                text = node[1][0]
                score = node[1][1]
                items.append({"text": str(text), "confidence": float(score or 0), "bbox": normalize_bbox(box)})
                return
            for child in node:
                visit(child)

        visit(result or [])
        return items

    def _looks_like_ocr_line(self, node: List[Any]) -> bool:
        if len(node) < 2 or not isinstance(node[1], list) or len(node[1]) < 2:
            return False
        return isinstance(node[1][0], str) and isinstance(node[1][1], Number)


engine = PaddleOCREngine()
if bool(CONFIG["ocr"].get("offline", False)):
    engine.ensure_loaded(str(CONFIG["ocr"].get("language", "zh-CN")))
ocr_semaphore = threading.BoundedSemaphore(max(1, int(CONFIG["ocr"].get("max_concurrency", 2))))
job_store = OCRJobStore()
app = FastAPI(title="ocr-service")


@app.exception_handler(RequestValidationError)
async def validation_exception_handler(_, exc: RequestValidationError):
    return JSONResponse(status_code=400, content={"error": "invalid_request", "message": str(exc)})


@app.exception_handler(Exception)
async def unhandled_exception_handler(_, exc: Exception):
    logger.exception("unhandled ocr-service exception")
    return JSONResponse(status_code=500, content={"error": "internal_error", "message": str(exc)})


@app.get("/healthz")
async def healthz() -> Dict[str, Any]:
    return {
        "status": "ok",
        "service": "ocr-service",
        "engine": str(CONFIG["ocr"]["engine"]),
        "model_loaded": engine.model_loaded,
        "cache_dir": str(CONFIG["ocr"].get("model_cache_dir") or os.environ.get("PADDLEX_HOME", "default")),
        "offline": bool(CONFIG["ocr"].get("offline", False)),
    }


@app.get("/readyz")
async def readyz():
    content = await healthz()
    if not engine.model_loaded:
        content["status"] = "loading"
        return JSONResponse(status_code=503, content=content)
    content["status"] = "ready"
    return content


@app.post("/v1/ocr/keyframes")
async def ocr_keyframes(req: OCRRequest):
    error = validate_ocr_request(req)
    if error:
        return error
    started = time.monotonic()
    try:
        return await asyncio.to_thread(process_ocr_request_with_limit, req)
    finally:
        logger.info(
            "ocr request finished task_id=%s keyframes=%d latency_ms=%d",
            req.task_id,
            len(req.keyframes),
            int((time.monotonic() - started) * 1000),
        )


@app.post("/v1/ocr/keyframe")
async def ocr_keyframe(req: SingleOCRRequest):
    batch_req = OCRRequest(
        task_id=req.task_id,
        language=req.language,
        min_confidence=req.min_confidence,
        dedupe=False,
        keyframes=[req.keyframe],
    )
    error = validate_ocr_request(batch_req)
    if error:
        return error
    started = time.monotonic()
    try:
        return await asyncio.to_thread(process_ocr_request_with_limit, batch_req)
    finally:
        logger.info(
            "single ocr request finished task_id=%s keyframe_id=%s latency_ms=%d",
            req.task_id,
            req.keyframe.id,
            int((time.monotonic() - started) * 1000),
        )


@app.post("/v1/ocr/keyframes/jobs")
async def submit_ocr_keyframes_job(req: OCRRequest):
    error = validate_ocr_request(req)
    if error:
        return error

    job = job_store.create(req.task_id)
    asyncio.create_task(run_ocr_job(job.id, req))
    return {"job_id": job.id, "task_id": req.task_id, "status": job.status}


@app.get("/v1/ocr/keyframes/jobs/result")
async def get_ocr_keyframes_job_result(id: str):
    job = job_store.get(id.strip())
    if not job:
        return JSONResponse(status_code=404, content={"error": "not_found", "message": "ocr job not found"})

    response: Dict[str, Any] = {
        "job_id": job.id,
        "task_id": job.task_id,
        "status": job.status,
    }
    if job.status == "done" and job.result is not None:
        response.update(job.result)
    if job.status == "failed":
        response["error_code"] = job.error_code
        response["error_message"] = job.error_message
    return response


async def run_ocr_job(job_id: str, req: OCRRequest) -> None:
    started = time.monotonic()
    try:
        result = await asyncio.to_thread(process_ocr_request_with_limit, req)
        job_store.mark_done(job_id, result)
        logger.info(
            "ocr job done job_id=%s task_id=%s keyframes=%d latency_ms=%d",
            job_id,
            req.task_id,
            len(req.keyframes),
            int((time.monotonic() - started) * 1000),
        )
    except Exception as exc:
        logger.exception("ocr job failed job_id=%s task_id=%s", job_id, req.task_id)
        job_store.mark_failed(job_id, "ocr_failed", str(exc))


def validate_ocr_request(req: OCRRequest) -> Optional[JSONResponse]:
    if not req.keyframes:
        return JSONResponse(status_code=400, content={"error": "empty_keyframes", "message": "keyframes must not be empty"})

    max_images = int(CONFIG["ocr"].get("max_images_per_request", 120))
    if len(req.keyframes) > max_images:
        return JSONResponse(status_code=400, content={"error": "too_many_images", "message": "max_images_per_request exceeded"})
    return None


def process_ocr_request_with_limit(req: OCRRequest) -> Dict[str, Any]:
    with ocr_semaphore:
        return process_ocr_request(req)


def process_ocr_request(req: OCRRequest) -> Dict[str, Any]:
    min_confidence = float(req.min_confidence if req.min_confidence is not None else CONFIG["ocr"]["min_confidence"])
    dedupe = bool(req.dedupe if req.dedupe is not None else CONFIG["ocr"].get("dedupe", True))
    dedupe_window = float(CONFIG["ocr"].get("dedupe_window_seconds", 2.0))
    warnings: List[Dict[str, str]] = []
    ocr_items: List[Dict[str, Any]] = []
    last_seen: Dict[str, float] = {}

    for keyframe in req.keyframes:
        path, error = resolve_allowed_file(keyframe.local_path)
        if error:
            warnings.append(
                {
                    "type": "ocr_image_failed",
                    "keyframe_id": keyframe.id,
                    "local_path": keyframe.local_path,
                    "message": error,
                }
            )
            continue
        try:
            raw_items = engine.recognize(path, req.language)
        except Exception as exc:
            logger.exception("ocr failed task_id=%s keyframe_id=%s path=%s", req.task_id, keyframe.id, path)
            warnings.append(
                {
                    "type": "ocr_image_failed",
                    "keyframe_id": keyframe.id,
                    "local_path": keyframe.local_path,
                    "message": str(exc),
                }
            )
            continue

        kept_count = 0
        for raw in raw_items:
            text = str(raw.get("text") or "").strip()
            confidence = float(raw.get("confidence") or 0)
            if not text or confidence < min_confidence:
                continue
            if dedupe and len(text) > 1:
                prev_time = last_seen.get(text)
                if prev_time is not None and abs(float(keyframe.time) - prev_time) < dedupe_window:
                    continue
                last_seen[text] = float(keyframe.time)
            kept_count += 1
            ocr_items.append(
                {
                    "segment_id": keyframe.segment_id,
                    "keyframe_id": keyframe.id,
                    "time": keyframe.time,
                    "text": text,
                    "bbox": raw.get("bbox") or [],
                    "confidence": confidence,
                    "source": "ocr",
                }
            )
        logger.info(
            "ocr keyframe processed task_id=%s keyframe_id=%s raw_items=%d kept_items=%d min_confidence=%.2f",
            req.task_id,
            keyframe.id,
            len(raw_items),
            kept_count,
            min_confidence,
        )

    return {"ocr_items": ocr_items, "warnings": warnings}
