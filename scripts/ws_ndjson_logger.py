#!/usr/bin/env python3
"""Subscribe to one hub WebSocket topic and append wrapper events as NDJSON."""

from __future__ import annotations

import argparse
import json
import os
import signal
import time
from datetime import datetime, timezone
from pathlib import Path

import websocket


RUNNING = True


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--topic", required=True, help="Hub WebSocket topic to subscribe to")
    parser.add_argument("--output", required=True, help="NDJSON file to append received wrapper messages to")
    parser.add_argument("--env-file", help="Optional dotenv-style file loaded before resolving HUB_* settings")
    parser.add_argument("--ws-url", default=os.getenv("HUB_WS_URL"), help="Hub WebSocket URL")
    parser.add_argument("--token", default=os.getenv("HUB_TOKEN"), help="Optional bearer token for subscribe params")
    parser.add_argument(
        "--reconnect-delay-seconds",
        type=float,
        default=2.0,
        help="Delay before reconnecting after a receive failure",
    )
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
                time.sleep(max(args.reconnect_delay_seconds, 0.0))
            finally:
                if connection is not None:
                    connection.close()
    return 0


def load_env_file(path: str | None) -> None:
    if not path:
        return
    input_path = Path(path)
    if not input_path.exists():
        return
    with input_path.open("r", encoding="utf-8") as handle:
        for line in handle:
            stripped = line.strip()
            if not stripped or stripped.startswith("#") or "=" not in stripped:
                continue
            key, value = stripped.split("=", 1)
            os.environ.setdefault(key.strip(), value.strip())


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
