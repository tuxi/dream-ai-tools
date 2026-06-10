import argparse
import os
import sys
from pathlib import Path

import uvicorn


def load_app():
    if __package__ in ("", None):
        sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
        from asr_service.main import app, CONFIG
    else:
        from .main import app, CONFIG
    return app, CONFIG


def main() -> None:
    parser = argparse.ArgumentParser(description="Run asr-service")
    parser.add_argument("--config", default=os.environ.get("CONFIG_PATH", "config.yaml"))
    args = parser.parse_args()
    os.environ["CONFIG_PATH"] = args.config

    app, config = load_app()
    uvicorn.run(app, host=config["server"]["host"], port=int(config["server"]["port"]))


if __name__ == "__main__":
    main()
