import argparse
import os
from pathlib import Path
from typing import Any, Dict

import yaml


DEFAULT_CONFIG: Dict[str, Any] = {
    "ocr": {
        "lang": "ch",
        "ocr_version": "PP-OCRv5",
        "use_doc_orientation_classify": False,
        "use_doc_unwarping": False,
        "use_textline_orientation": False,
        "model_cache_dir": "/models/paddlex",
        "offline": False,
    }
}


def deep_merge(base: Dict[str, Any], override: Dict[str, Any]) -> Dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        if isinstance(value, dict) and isinstance(merged.get(key), dict):
            merged[key] = deep_merge(merged[key], value)
        else:
            merged[key] = value
    return merged


def load_config(path: str) -> Dict[str, Any]:
    if not path or not Path(path).exists():
        return DEFAULT_CONFIG
    with open(path, "r", encoding="utf-8") as f:
        data = yaml.safe_load(f) or {}
    return deep_merge(DEFAULT_CONFIG, data)


def setup_cache(config: Dict[str, Any]) -> None:
    os.environ.setdefault("FLAGS_use_mkldnn", "0")
    os.environ.setdefault("FLAGS_enable_pir_api", "0")
    cache_dir = str(config["ocr"].get("model_cache_dir") or "/models/paddlex")
    root = "/models" if cache_dir.startswith("/models") else str(Path.home() / ".cache/dream-ai")
    os.environ["XDG_CACHE_HOME"] = os.environ.get("XDG_CACHE_HOME", str(Path(root) / "cache"))
    os.environ["PADDLE_HOME"] = os.environ.get("PADDLE_HOME", str(Path(root) / "paddle"))
    os.environ["PADDLEX_HOME"] = os.environ.get("PADDLEX_HOME", cache_dir)
    os.environ["PADDLEOCR_HOME"] = os.environ.get("PADDLEOCR_HOME", str(Path(root) / "paddleocr"))
    Path(cache_dir).mkdir(parents=True, exist_ok=True)
    setup_paddlex_home_link(cache_dir)


def setup_paddlex_home_link(cache_dir: str) -> None:
    target = Path(cache_dir)
    legacy = Path.home() / ".paddlex"
    if legacy.is_symlink() and legacy.resolve(strict=False) == target.resolve(strict=False):
        return
    if legacy.is_mount():
        return
    if legacy.exists():
        if legacy.is_dir() and not any(legacy.iterdir()):
            legacy.rmdir()
        else:
            print(f"Warning: {legacy} exists, PaddleX may use it instead of {target}")
            return
    legacy.symlink_to(target, target_is_directory=True)


def main() -> None:
    parser = argparse.ArgumentParser(description="Download OCR models into configured cache")
    parser.add_argument("--target", choices=["ocr"], default="ocr")
    parser.add_argument("--config", default=os.environ.get("CONFIG_PATH", "/app/config.yaml"))
    args = parser.parse_args()

    config = load_config(args.config)
    config["ocr"]["offline"] = False
    setup_cache(config)

    from paddleocr import PaddleOCR

    lang = str(config["ocr"].get("lang", "ch"))
    ocr_version = str(config["ocr"].get("ocr_version", "PP-OCRv5"))
    stable_kwargs = {
        "use_doc_orientation_classify": bool(config["ocr"].get("use_doc_orientation_classify", False)),
        "use_doc_unwarping": bool(config["ocr"].get("use_doc_unwarping", False)),
        "use_textline_orientation": bool(config["ocr"].get("use_textline_orientation", False)),
    }
    candidates = [
        {"lang": lang, "ocr_version": ocr_version, **stable_kwargs},
        {"lang": lang, **stable_kwargs},
        {"lang": lang, "ocr_version": ocr_version},
        {"lang": lang},
    ]
    print("Downloading PaddleOCR models with cache:", os.environ.get("PADDLEX_HOME"))
    last_error = None
    for kwargs in candidates:
        try:
            PaddleOCR(**kwargs)
            break
        except Exception as exc:
            last_error = exc
            message = str(exc).lower()
            if "unknown argument" in message or "unexpected keyword" in message or isinstance(exc, TypeError):
                continue
            raise
    else:
        raise RuntimeError(f"failed to initialize PaddleOCR: {last_error}")
    print("OCR models ready under:", os.environ.get("PADDLEX_HOME"))


if __name__ == "__main__":
    main()
