import argparse
import os
from pathlib import Path
from typing import Any, Dict

import yaml


DEFAULT_CONFIG: Dict[str, Any] = {
    "asr": {
        "model": "auto",
        "model_cache_dir": "/models/modelscope",
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
    cache_dir = str(config["asr"].get("model_cache_dir") or "/models/modelscope")
    root = "/models" if cache_dir.startswith("/models") else str(Path.home() / ".cache/dream-ai")
    os.environ["XDG_CACHE_HOME"] = os.environ.get("XDG_CACHE_HOME", str(Path(root) / "cache"))
    os.environ["HF_HOME"] = os.environ.get("HF_HOME", str(Path(root) / "huggingface"))
    os.environ["HF_HUB_CACHE"] = os.environ.get("HF_HUB_CACHE", str(Path(root) / "huggingface/hub"))
    os.environ["MODELSCOPE_CACHE"] = os.environ.get("MODELSCOPE_CACHE", cache_dir)
    os.environ["TORCH_HOME"] = os.environ.get("TORCH_HOME", str(Path(root) / "torch"))
    Path(cache_dir).mkdir(parents=True, exist_ok=True)


def resolve_model_name(value: str) -> str:
    if not value or value == "auto" or value == "funasr":
        return "paraformer-zh"
    if value == "sensevoice":
        return "iic/SenseVoiceSmall"
    return value


def main() -> None:
    parser = argparse.ArgumentParser(description="Download ASR models into configured cache")
    parser.add_argument("--target", choices=["asr"], default="asr")
    parser.add_argument("--config", default=os.environ.get("CONFIG_PATH", "/app/config.yaml"))
    args = parser.parse_args()

    config = load_config(args.config)
    config["asr"]["offline"] = False
    setup_cache(config)

    from funasr import AutoModel

    model_name = resolve_model_name(str(config["asr"].get("model", "auto")))
    kwargs: Dict[str, Any] = {"model": model_name, "disable_update": True}
    if model_name != "iic/SenseVoiceSmall":
        kwargs.update({"vad_model": "fsmn-vad", "punc_model": "ct-punc"})

    print("Downloading FunASR models with cache:", os.environ.get("MODELSCOPE_CACHE"))
    try:
        AutoModel(**kwargs)
    except TypeError:
        kwargs.pop("disable_update", None)
        AutoModel(**kwargs)
    print("ASR models ready under:", os.environ.get("MODELSCOPE_CACHE"))


if __name__ == "__main__":
    main()
