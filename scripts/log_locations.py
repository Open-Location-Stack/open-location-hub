#!/usr/bin/env python3
"""Log location_updates messages to an NDJSON file."""

from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="logs/location_updates.ndjson")
    parser.add_argument("--env-file")
    parser.add_argument("--ws-url")
    parser.add_argument("--token")
    args = parser.parse_args()

    command = [
        sys.executable,
        str(Path(__file__).with_name("ws_ndjson_logger.py")),
        "--topic",
        "location_updates",
        "--output",
        args.output,
    ]
    if args.env_file:
        command.extend(["--env-file", args.env_file])
    if args.ws_url:
        command.extend(["--ws-url", args.ws_url])
    if args.token:
        command.extend(["--token", args.token])
    try:
        return subprocess.call(command)
    except KeyboardInterrupt:
        return 0


if __name__ == "__main__":
    raise SystemExit(main())
