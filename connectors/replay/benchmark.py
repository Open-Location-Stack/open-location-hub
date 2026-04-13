#!/usr/bin/env python3
"""Run repeatable replay stress benchmarks against a local hub."""

from __future__ import annotations

import argparse
import csv
import json
import os
import re
import signal
import subprocess
import sys
import threading
import time
from dataclasses import asdict, dataclass
from datetime import UTC, datetime
from pathlib import Path

import websocket

from replay_support import load_env_file, load_logged_locations, preview_replay_schedule


DEFAULT_DATASET = "connectors/replay/benchmarks/opensky-germany-2026-04-08/location_updates.ndjson"
DEFAULT_FREQUENCIES = (1.0, 2.0, 4.0, 10.0)
DEFAULT_TARGET_DURATION_SECONDS = 30.0
DEFAULT_TIMEOUT_SECONDS = 35.0
DEFAULT_BATCH_WINDOW_MS = 25.0
DEFAULT_MAX_BATCH_SIZE = 256
DEFAULT_CONTAINER_NAME = "local-hub-hub-1"
DEFAULT_TRACKABLE_RADIUS_METERS = 50.0
DEFAULT_OBSERVER_DRAIN_SECONDS = 1.0
BENCHMARK_TOPICS = ("location_updates", "trackable_motions", "fence_events", "collision_events")

DROP_PATTERNS = {
    "native": re.compile(r'"msg":"native location queue full; dropping location work".*"dropped":(\d+)'),
    "decision": re.compile(r'"msg":"decision location queue full; dropping location work".*"dropped":(\d+)'),
}


@dataclass(frozen=True)
class ScheduleStats:
    interpolation_rate_hz: float
    logged_locations: int
    scheduled_locations: int
    synthetic_locations: int
    source_span_seconds: float
    expected_runtime_seconds: float
    acceleration_factor: float


@dataclass(frozen=True)
class BenchmarkRow:
    suite_started_at: str
    run_started_at: str
    run_finished_at: str
    dataset: str
    interpolation_rate_hz: float
    acceleration_factor: float
    target_duration_seconds: float
    timeout_seconds: float
    source_span_seconds: float
    expected_runtime_seconds: float
    logged_locations: int
    scheduled_locations: int
    synthetic_locations: int
    actual_runtime_seconds: float
    completed: bool
    did_not_finish: bool
    exit_code: int | None
    native_drop_delta: int
    decision_drop_delta: int
    total_drop_delta: int
    drop_free: bool
    collision_topic_enabled: bool
    raw_location_updates: int
    trackable_updates: int
    geofence_events: int
    geofence_entries: int
    geofence_exits: int
    collisions: int
    collision_free: bool
    average_replayed_locations_per_second: float
    log_path: str


@dataclass(frozen=True)
class BenchmarkObservation:
    collision_topic_enabled: bool
    raw_location_updates: int
    trackable_updates: int
    geofence_events: int
    geofence_entries: int
    geofence_exits: int
    collisions: int


class BenchmarkObserver:
    def __init__(self, ws_url: str, token: str | None, timeout_seconds: float = 30.0):
        self._ws_url = ws_url
        self._token = token
        self._timeout_seconds = timeout_seconds
        self._connection: websocket.WebSocket | None = None
        self._thread: threading.Thread | None = None
        self._stop = threading.Event()
        self._error: Exception | None = None
        self._collision_topic_enabled = True
        self._raw_location_updates = 0
        self._trackable_updates = 0
        self._geofence_entries = 0
        self._geofence_exits = 0
        self._collisions = 0

    def start(self) -> None:
        self._connection = websocket.create_connection(self._ws_url, timeout=self._timeout_seconds)
        self._connection.settimeout(1.0)
        for topic in BENCHMARK_TOPICS:
            subscribe: dict[str, object] = {"event": "subscribe", "topic": topic}
            if self._token:
                subscribe["params"] = {"token": self._token}
            self._connection.send(json.dumps(subscribe))
        self._thread = threading.Thread(target=self._recv_loop, name="replay-benchmark-observer", daemon=True)
        self._thread.start()

    def stop(self, drain_seconds: float) -> BenchmarkObservation:
        if drain_seconds > 0:
            time.sleep(drain_seconds)
        self._stop.set()
        if self._connection is not None:
            try:
                self._connection.close()
            except Exception:
                pass
        if self._thread is not None:
            self._thread.join(timeout=5.0)
        if self._error is not None:
            raise RuntimeError(f"benchmark observer failed: {self._error}") from self._error
        geofence_events = self._geofence_entries + self._geofence_exits
        return BenchmarkObservation(
            collision_topic_enabled=self._collision_topic_enabled,
            raw_location_updates=self._raw_location_updates,
            trackable_updates=self._trackable_updates,
            geofence_events=geofence_events,
            geofence_entries=self._geofence_entries,
            geofence_exits=self._geofence_exits,
            collisions=self._collisions,
        )

    def _recv_loop(self) -> None:
        assert self._connection is not None
        while not self._stop.is_set():
            try:
                raw = self._connection.recv()
            except TimeoutError:
                self._ping()
                continue
            except websocket.WebSocketTimeoutException:
                self._ping()
                continue
            except websocket.WebSocketConnectionClosedException:
                if self._stop.is_set():
                    return
                self._error = RuntimeError("websocket connection closed unexpectedly")
                return
            except Exception as exc:
                if self._stop.is_set():
                    return
                self._error = exc
                return
            if isinstance(raw, bytes):
                raw = raw.decode("utf-8", errors="ignore")
            if not isinstance(raw, str):
                continue
            raw = raw.strip()
            if not raw or raw[0] not in "[{":
                continue
            try:
                message = json.loads(raw)
            except json.JSONDecodeError as exc:
                self._error = exc
                return
            self._handle_message(message)

    def _handle_message(self, message: dict[str, object]) -> None:
        topic = message.get("topic")
        event = message.get("event")
        if topic == "collision_events" and event == "error" and message.get("code") == 10002:
            self._collision_topic_enabled = False
            return
        if event != "message" or topic not in BENCHMARK_TOPICS:
            return
        payload = message.get("payload")
        if not isinstance(payload, list):
            return
        if topic == "location_updates":
            self._raw_location_updates += len(payload)
            return
        if topic == "trackable_motions":
            self._trackable_updates += len(payload)
            return
        if topic == "collision_events":
            self._collisions += len(payload)
            return
        if topic == "fence_events":
            for item in payload:
                if not isinstance(item, dict):
                    continue
                event_type = item.get("event_type")
                if event_type == "region_entry":
                    self._geofence_entries += 1
                elif event_type == "region_exit":
                    self._geofence_exits += 1

    def _ping(self) -> None:
        if self._connection is None:
            return
        try:
            self._connection.ping("keepalive")
        except Exception:
            if not self._stop.is_set():
                self._error = RuntimeError("websocket ping failed")


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", default=os.getenv("REPLAY_INPUT", DEFAULT_DATASET))
    parser.add_argument("--env-file", default=os.getenv("REPLAY_ENV_FILE"))
    parser.add_argument("--hub-http-url", default=os.getenv("HUB_HTTP_URL", "http://localhost:8080"))
    parser.add_argument("--hub-ws-url", default=os.getenv("HUB_WS_URL", "ws://localhost:8080/v2/ws/socket"))
    parser.add_argument("--hub-token", default=os.getenv("HUB_TOKEN"))
    parser.add_argument("--hub-container-name", default=os.getenv("HUB_DOCKER_CONTAINER", DEFAULT_CONTAINER_NAME))
    parser.add_argument("--target-duration-seconds", type=float, default=DEFAULT_TARGET_DURATION_SECONDS)
    parser.add_argument("--timeout-seconds", type=float, default=DEFAULT_TIMEOUT_SECONDS)
    parser.add_argument("--batch-window-ms", type=float, default=DEFAULT_BATCH_WINDOW_MS)
    parser.add_argument("--max-batch-size", type=int, default=DEFAULT_MAX_BATCH_SIZE)
    parser.add_argument(
        "--bootstrap-trackables",
        action=argparse.BooleanOptionalAction,
        default=(os.getenv("REPLAY_BOOTSTRAP_TRACKABLES", "true").strip().lower() not in {"0", "false", "no"}),
        help="Create or update referenced trackables before each replay run.",
    )
    parser.add_argument(
        "--trackable-radius-meters",
        type=float,
        default=float(os.getenv("REPLAY_TRACKABLE_RADIUS_METERS", str(DEFAULT_TRACKABLE_RADIUS_METERS))),
        help="Trackable radius passed to the replay connector. Defaults to 50 m for the OpenSky benchmark dataset.",
    )
    parser.add_argument(
        "--observer-drain-seconds",
        type=float,
        default=DEFAULT_OBSERVER_DRAIN_SECONDS,
        help="Extra time to keep the benchmark observer running after replay exits so derived events can drain.",
    )
    parser.add_argument(
        "--rates",
        nargs="+",
        type=float,
        default=list(DEFAULT_FREQUENCIES),
        help="Interpolation rates in Hertz to benchmark.",
    )
    parser.add_argument(
        "--reports-dir",
        default="connectors/replay/reports",
        help="Directory for CSV summaries and per-run logs.",
    )
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)

    if args.target_duration_seconds <= 0:
        raise SystemExit("--target-duration-seconds must be greater than 0")
    if args.timeout_seconds <= 0:
        raise SystemExit("--timeout-seconds must be greater than 0")
    if args.timeout_seconds <= args.target_duration_seconds:
        raise SystemExit("--timeout-seconds must be greater than --target-duration-seconds")
    if args.trackable_radius_meters <= 0:
        raise SystemExit("--trackable-radius-meters must be greater than 0")
    if args.observer_drain_seconds < 0:
        raise SystemExit("--observer-drain-seconds must be greater than or equal to 0")

    dataset = Path(args.input).resolve()
    if not dataset.exists():
        raise SystemExit(f"dataset not found: {dataset}")

    token = args.hub_token or fetch_demo_token()
    if not token:
        raise SystemExit("HUB_TOKEN is required or local-hub/fetch_demo_token.sh must be available")

    reports_dir = Path(args.reports_dir).resolve()
    reports_dir.mkdir(parents=True, exist_ok=True)

    suite_started_at = datetime.now(UTC)
    suite_stamp = suite_started_at.strftime("%Y%m%dT%H%M%SZ")
    csv_path = reports_dir / f"benchmark-{suite_stamp}.csv"

    logged_locations = load_logged_locations(str(dataset))
    rows: list[BenchmarkRow] = []

    for rate in args.rates:
        stats = compute_schedule_stats(logged_locations, rate, args.target_duration_seconds)
        run_prefix = f"{suite_stamp}-{format_rate(rate)}hz"
        log_path = reports_dir / f"{run_prefix}.log"

        baseline = latest_drop_counters(args.hub_container_name)
        started_at = datetime.now(UTC)
        observer = BenchmarkObserver(args.hub_ws_url, token)
        observer.start()
        try:
            actual_runtime_seconds, completed, exit_code = run_replay(
                dataset=dataset,
                hub_http_url=args.hub_http_url,
                hub_ws_url=args.hub_ws_url,
                hub_token=token,
                interpolation_rate_hz=rate,
                acceleration_factor=stats.acceleration_factor,
                batch_window_ms=args.batch_window_ms,
                max_batch_size=args.max_batch_size,
                bootstrap_trackables=args.bootstrap_trackables,
                trackable_radius_meters=args.trackable_radius_meters,
                timeout_seconds=args.timeout_seconds,
                log_path=log_path,
            )
        finally:
            observed = observer.stop(args.observer_drain_seconds)
        finished_at = datetime.now(UTC)
        after = latest_drop_counters(args.hub_container_name)

        native_drop_delta = after["native"] - baseline["native"]
        decision_drop_delta = after["decision"] - baseline["decision"]
        total_drop_delta = native_drop_delta + decision_drop_delta

        rows.append(
            BenchmarkRow(
                suite_started_at=suite_started_at.isoformat(),
                run_started_at=started_at.isoformat(),
                run_finished_at=finished_at.isoformat(),
                dataset=str(dataset),
                interpolation_rate_hz=rate,
                acceleration_factor=stats.acceleration_factor,
                target_duration_seconds=args.target_duration_seconds,
                timeout_seconds=args.timeout_seconds,
                source_span_seconds=stats.source_span_seconds,
                expected_runtime_seconds=stats.expected_runtime_seconds,
                logged_locations=stats.logged_locations,
                scheduled_locations=stats.scheduled_locations,
                synthetic_locations=stats.synthetic_locations,
                actual_runtime_seconds=actual_runtime_seconds,
                completed=completed,
                did_not_finish=not completed,
                exit_code=exit_code,
                native_drop_delta=native_drop_delta,
                decision_drop_delta=decision_drop_delta,
                total_drop_delta=total_drop_delta,
                drop_free=(total_drop_delta == 0),
                collision_topic_enabled=observed.collision_topic_enabled,
                raw_location_updates=observed.raw_location_updates,
                trackable_updates=observed.trackable_updates,
                geofence_events=observed.geofence_events,
                geofence_entries=observed.geofence_entries,
                geofence_exits=observed.geofence_exits,
                collisions=observed.collisions,
                collision_free=(observed.collisions == 0),
                average_replayed_locations_per_second=(
                    stats.scheduled_locations / actual_runtime_seconds if actual_runtime_seconds > 0 else 0.0
                ),
                log_path=str(log_path),
            )
        )

    write_csv(csv_path, rows)
    latest_path = reports_dir / "latest.csv"
    write_csv(latest_path, rows)
    print(f"wrote benchmark report to {csv_path}")
    print(f"updated latest report at {latest_path}")
    return 0


def compute_schedule_stats(
    logged_locations: list,
    interpolation_rate_hz: float,
    target_duration_seconds: float,
) -> ScheduleStats:
    preview = preview_replay_schedule(logged_locations, interpolation_rate_hz)
    source_span_seconds = preview.source_span_seconds
    acceleration_factor = max(source_span_seconds / target_duration_seconds, 1.0)
    expected_runtime_seconds = source_span_seconds / acceleration_factor if acceleration_factor > 0 else 0.0
    return ScheduleStats(
        interpolation_rate_hz=interpolation_rate_hz,
        logged_locations=len(logged_locations),
        scheduled_locations=preview.scheduled_locations,
        synthetic_locations=preview.synthetic_locations,
        source_span_seconds=source_span_seconds,
        expected_runtime_seconds=expected_runtime_seconds,
        acceleration_factor=acceleration_factor,
    )


def run_replay(
    *,
    dataset: Path,
    hub_http_url: str,
    hub_ws_url: str,
    hub_token: str,
    interpolation_rate_hz: float,
    acceleration_factor: float,
    batch_window_ms: float,
    max_batch_size: int,
    bootstrap_trackables: bool,
    trackable_radius_meters: float,
    timeout_seconds: float,
    log_path: Path,
) -> tuple[float, bool, int | None]:
    command = [
        sys.executable,
        "connectors/replay/connector.py",
        "--input",
        str(dataset),
        "--interpolation-rate-hz",
        str(interpolation_rate_hz),
        "--acceleration-factor",
        str(acceleration_factor),
        "--batch-window-ms",
        str(batch_window_ms),
        "--max-batch-size",
        str(max_batch_size),
        "--trackable-radius-meters",
        str(trackable_radius_meters),
    ]
    if not bootstrap_trackables:
        command.append("--no-bootstrap-trackables")
    environment = os.environ.copy()
    environment["HUB_HTTP_URL"] = hub_http_url
    environment["HUB_WS_URL"] = hub_ws_url
    environment["HUB_TOKEN"] = hub_token

    started = time.monotonic()
    with log_path.open("w", encoding="utf-8") as handle:
        handle.write(f"# command: {' '.join(command)}\n")
        handle.write(f"# started_at: {datetime.now(UTC).isoformat()}\n")
        handle.flush()

        process = subprocess.Popen(
            command,
            cwd=Path(__file__).resolve().parents[2],
            env=environment,
            stdout=handle,
            stderr=subprocess.STDOUT,
            text=True,
            start_new_session=True,
        )
        completed = False
        exit_code: int | None = None
        try:
            exit_code = process.wait(timeout=timeout_seconds)
            completed = exit_code == 0
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
            exit_code = None
        finally:
            handle.write(f"# finished_at: {datetime.now(UTC).isoformat()}\n")

    return time.monotonic() - started, completed, exit_code


def latest_drop_counters(container_name: str) -> dict[str, int]:
    command = ["docker", "logs", container_name]
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=False)
    except FileNotFoundError as exc:
        raise RuntimeError("docker CLI is required for benchmark drop-counter collection") from exc
    if result.returncode != 0:
        raise RuntimeError(f"docker logs failed for {container_name}: {result.stderr.strip()}")

    counters = {"native": 0, "decision": 0}
    for line in result.stdout.splitlines():
        for key, pattern in DROP_PATTERNS.items():
            match = pattern.search(line)
            if match:
                counters[key] = int(match.group(1))
    return counters


def fetch_demo_token() -> str | None:
    script = Path("local-hub/fetch_demo_token.sh")
    if not script.exists():
        return None
    result = subprocess.run([str(script)], capture_output=True, text=True, check=False)
    if result.returncode != 0:
        return None
    try:
        payload = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    token = payload.get("access_token")
    return token if isinstance(token, str) and token else None


def write_csv(path: Path, rows: list[BenchmarkRow]) -> None:
    if not rows:
        return
    with path.open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(asdict(rows[0]).keys()))
        writer.writeheader()
        for row in rows:
            writer.writerow(asdict(row))


def format_rate(rate: float) -> str:
    if float(rate).is_integer():
        return str(int(rate))
    return str(rate).replace(".", "_")


if __name__ == "__main__":
    raise SystemExit(main())
