#!/usr/bin/env python3
"""Subscribe to one hub WebSocket topic and append wrapper events as NDJSON."""

from __future__ import annotations

import argparse
import json
import os
import signal
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

import websocket

SCRIPT_DIR = Path(__file__).resolve().parent
ROOT_DIR = SCRIPT_DIR.parent
sys.path.insert(0, str(ROOT_DIR))

from opensky_support import load_env_file  # noqa: E402


RUNNING = True


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--topic", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--env-file", default=os.getenv("OPENSKY_ENV_FILE"))
    parser.add_argument("--ws-url", default=os.getenv("HUB_WS_URL"))
    parser.add_argument("--token", default=os.getenv("HUB_TOKEN"))
    return parser


def main() -> int:
    global RUNNING
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)
    ws_url = args.ws_url or os.getenv("HUB_WS_URL")
    if not ws_url:
        raise SystemExit("HUB_WS_URL or --ws-url is required")
    output_path = Path(args.output)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    signal.signal(signal.SIGINT, handle_stop)
    signal.signal(signal.SIGTERM, handle_stop)

    token = args.token or os.getenv("HUB_TOKEN")
    with output_path.open("a", encoding="utf-8") as handle:
        while RUNNING:
            connection: websocket.WebSocket | None = None
            try:
                connection = connect_and_subscribe(ws_url, args.topic, token)
                while RUNNING:
                    try:
                        raw = connection.recv()
                    except TimeoutError:
                        connection.ping("keepalive")
                        continue
                    except websocket.WebSocketTimeoutException:
                        connection.ping("keepalive")
                        continue
                    payload = {
                        "received_at": datetime.now(timezone.utc).isoformat(),
                        "topic": args.topic,
                        "message": json.loads(raw),
                    }
                    handle.write(json.dumps(payload, separators=(",", ":")) + "\n")
                    handle.flush()
                    time.sleep(0.01)
            except KeyboardInterrupt:
                break
            except Exception:
                time.sleep(2.0)
            finally:
                if connection is not None:
                    connection.close()
    return 0


def handle_stop(_signum: int, _frame: object) -> None:
    global RUNNING
    RUNNING = False


def connect_and_subscribe(ws_url: str, topic: str, token: str | None) -> websocket.WebSocket:
    connection = websocket.create_connection(ws_url, timeout=30)
    connection.settimeout(15)
    subscribe = {"event": "subscribe", "topic": topic}
    if token:
        subscribe["params"] = {"token": token}
    connection.send(json.dumps(subscribe))
    return connection


if __name__ == "__main__":
    raise SystemExit(main())
