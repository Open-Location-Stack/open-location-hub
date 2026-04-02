#!/usr/bin/env python3
"""Log location_updates messages to an NDJSON file."""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="connectors/opensky/logs/location_updates.ndjson")
    parser.add_argument("--env-file", default="connectors/opensky/.env.local")
    args = parser.parse_args()
    logger = Path(__file__).with_name("ws_ndjson_logger.py")
    command = [sys.executable, str(logger), "--topic", "location_updates", "--output", args.output, "--env-file", args.env_file]
    try:
        return subprocess.call(command)
    except KeyboardInterrupt:
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
