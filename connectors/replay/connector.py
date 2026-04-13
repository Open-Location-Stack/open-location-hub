#!/usr/bin/env python3
"""Replay logged location NDJSON files into a local Open RTLS Hub."""

from __future__ import annotations

import argparse
import logging
import os
import time
from datetime import UTC, datetime
from typing import Iterator

from hub_client import HubConfig, HubRESTClient, HubWebSocketPublisher
from replay_support import build_replay_schedule, load_env_file, load_logged_locations


LOGGER = logging.getLogger("replay.connector")
DEFAULT_TRACKABLE_RADIUS_METERS = 2.0


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", default=os.getenv("REPLAY_INPUT"), help="Path to a location_updates NDJSON file")
    parser.add_argument("--env-file", default=os.getenv("REPLAY_ENV_FILE"))
    parser.add_argument(
        "--acceleration-factor",
        type=float,
        default=float(os.getenv("REPLAY_ACCELERATION_FACTOR", "1.0")),
        help="Replay speed multiplier. 1.0 is real time, 2.0 is twice as fast.",
    )
    parser.add_argument(
        "--interpolation-rate-hz",
        type=float,
        default=float(os.getenv("REPLAY_INTERPOLATION_RATE_HZ", "0.0")),
        help="Synthetic interpolation cadence per object in Hertz. 1.0 emits once per second.",
    )
    parser.add_argument(
        "--batch-window-ms",
        type=float,
        default=float(os.getenv("REPLAY_BATCH_WINDOW_MS", "25.0")),
        help="Group replay events due within this many milliseconds into one WebSocket publish.",
    )
    parser.add_argument(
        "--max-batch-size",
        type=int,
        default=int(os.getenv("REPLAY_MAX_BATCH_SIZE", "256")),
        help="Maximum number of locations to send in one replay publish.",
    )
    parser.add_argument(
        "--bootstrap-trackables",
        action=argparse.BooleanOptionalAction,
        default=(os.getenv("REPLAY_BOOTSTRAP_TRACKABLES", "true").strip().lower() not in {"0", "false", "no"}),
        help="Create or update referenced trackables over REST before replay when HUB_HTTP_URL is configured.",
    )
    parser.add_argument(
        "--trackable-radius-meters",
        type=float,
        default=float(os.getenv("REPLAY_TRACKABLE_RADIUS_METERS", str(DEFAULT_TRACKABLE_RADIUS_METERS))),
        help="Trackable radius applied during REST bootstrap. Defaults to 2 m for indoor-style replay datasets.",
    )
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)

    if not args.input:
        raise SystemExit("--input or REPLAY_INPUT is required")
    if args.acceleration_factor <= 0:
        raise SystemExit("--acceleration-factor must be greater than 0")
    if args.interpolation_rate_hz < 0:
        raise SystemExit("--interpolation-rate-hz must be greater than or equal to 0")
    if args.batch_window_ms < 0:
        raise SystemExit("--batch-window-ms must be greater than or equal to 0")
    if args.max_batch_size <= 0:
        raise SystemExit("--max-batch-size must be greater than 0")
    if args.trackable_radius_meters <= 0:
        raise SystemExit("--trackable-radius-meters must be greater than 0")

    logging.basicConfig(
        level=os.getenv("LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    hub_config = HubConfig(
        http_url=(os.getenv("HUB_HTTP_URL") or "").strip() or None,
        ws_url=require_env("HUB_WS_URL"),
        token=os.getenv("HUB_TOKEN") or None,
    )
    hub_rest = HubRESTClient(hub_config)
    hub_ws = HubWebSocketPublisher(hub_config)

    logged_locations = load_logged_locations(args.input)
    replay_start = datetime.now(UTC)
    replay_schedule = build_replay_schedule(
        logged_locations=logged_locations,
        replay_start=replay_start,
        acceleration_factor=args.acceleration_factor,
        interpolation_rate_hz=args.interpolation_rate_hz,
    )

    LOGGER.info(
        "loaded %d logged locations and scheduled %d replay emissions starting at %s",
        len(logged_locations),
        len(replay_schedule),
        replay_start.isoformat(),
    )

    ensure_hub_resources(
        hub_rest,
        logged_locations,
        bootstrap_trackables=args.bootstrap_trackables,
        trackable_radius_meters=args.trackable_radius_meters,
    )

    start_monotonic = time.monotonic()
    try:
        for batch in iter_replay_batches(replay_schedule, args.batch_window_ms / 1000.0, args.max_batch_size):
            wait_until_scheduled(start_monotonic, replay_start, batch[0].replay_timestamp)
            hub_ws.publish_locations([event.location for event in batch])
            LOGGER.debug(
                "published replay batch size=%d first_timestamp=%s last_timestamp=%s",
                len(batch),
                batch[0].location.get("timestamp_generated"),
                batch[-1].location.get("timestamp_generated"),
            )
    except KeyboardInterrupt:
        LOGGER.info("stopping replay connector")
        return 0
    finally:
        hub_ws.close()

    LOGGER.info("replayed %d location updates from %s", len(replay_schedule), args.input)
    return 0


def ensure_hub_resources(
    hub_rest: HubRESTClient,
    logged_locations: list,
    *,
    bootstrap_trackables: bool,
    trackable_radius_meters: float,
) -> None:
    if not hub_rest.config.http_url:
        LOGGER.info("HUB_HTTP_URL not set; skipping provider and trackable bootstrap")
        return

    known_providers: set[str] = set()
    known_trackables: set[str] = set()
    for item in logged_locations:
        location = item.location
        provider_id = location.get("provider_id")
        provider_type = location.get("provider_type") or "replay"
        if isinstance(provider_id, str) and provider_id and provider_id not in known_providers:
            hub_rest.ensure_provider(
                provider_id=provider_id,
                provider_type=str(provider_type),
                name=provider_id,
                properties={"connector": "replay"},
            )
            known_providers.add(provider_id)

        trackables = location.get("trackables")
        if not bootstrap_trackables:
            continue
        if not isinstance(trackables, list) or not isinstance(provider_id, str) or not provider_id:
            continue
        for trackable_id in trackables:
            if not isinstance(trackable_id, str) or not trackable_id or trackable_id in known_trackables:
                continue
            hub_rest.ensure_trackable(
                trackable_id=trackable_id,
                name=trackable_name(location, trackable_id),
                provider_id=provider_id,
                radius=trackable_radius_meters,
                properties=trackable_properties(location),
            )
            known_trackables.add(trackable_id)

    if bootstrap_trackables:
        LOGGER.info("bootstrapped %d providers and %d trackables with radius %.2f m", len(known_providers), len(known_trackables), trackable_radius_meters)
    else:
        LOGGER.info("bootstrapped %d providers and skipped trackable bootstrap by configuration", len(known_providers))


def trackable_name(location: dict[str, object], trackable_id: str) -> str:
    properties = location.get("properties")
    if isinstance(properties, dict):
        for key in ("vehicle_label", "vehicle_id", "callsign", "icao24", "external_vehicle_id"):
            value = properties.get(key)
            if isinstance(value, str) and value:
                return value
    source = location.get("source")
    if isinstance(source, str) and source:
        return source
    return trackable_id


def trackable_properties(location: dict[str, object]) -> dict[str, object]:
    properties = location.get("properties")
    if isinstance(properties, dict):
        merged = dict(properties)
    else:
        merged = {}
    merged["connector"] = "replay"
    return merged


def wait_until_scheduled(start_monotonic: float, replay_start: datetime, replay_timestamp: datetime) -> None:
    delay_seconds = (replay_timestamp - replay_start).total_seconds()
    target_monotonic = start_monotonic + max(delay_seconds, 0.0)
    while True:
        remaining = target_monotonic - time.monotonic()
        if remaining <= 0:
            return
        time.sleep(min(remaining, 0.25))


def iter_replay_batches(replay_schedule: list, batch_window_seconds: float, max_batch_size: int) -> Iterator[list]:
    if not replay_schedule:
        return
    current_batch = [replay_schedule[0]]
    batch_start = replay_schedule[0].replay_timestamp
    for event in replay_schedule[1:]:
        same_window = (event.replay_timestamp - batch_start).total_seconds() <= batch_window_seconds
        if same_window and len(current_batch) < max_batch_size:
            current_batch.append(event)
            continue
        yield current_batch
        current_batch = [event]
        batch_start = event.replay_timestamp
    yield current_batch


def require_env(name: str) -> str:
    value = os.getenv(name)
    if not value:
        raise SystemExit(f"{name} is required")
    return value


if __name__ == "__main__":
    raise SystemExit(main())
