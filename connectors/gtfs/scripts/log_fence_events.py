#!/usr/bin/env python3
"""Log fence_events messages to an NDJSON file."""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="connectors/gtfs/logs/fence_events.ndjson")
    parser.add_argument("--env-file", default="connectors/gtfs/.env.local")
    args = parser.parse_args()

    logger = Path(__file__).with_name("ws_ndjson_logger.py")
    command = [
        sys.executable,
        str(logger),
        "--topic",
        "fence_events",
        "--output",
        args.output,
        "--env-file",
        args.env_file,
    ]
    try:
        return subprocess.call(command)
    except KeyboardInterrupt:
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
