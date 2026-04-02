#!/usr/bin/env python3
"""Forward OpenSky aircraft state vectors to a local Open RTLS Hub over WebSocket."""

from __future__ import annotations

import argparse
import logging
import os
import time
from datetime import datetime, timezone

from hub_client import HubConfig, HubRESTClient, HubWebSocketPublisher, deterministic_uuid, point
from opensky_support import fetch_states, load_env_file, resolve_bbox


LOGGER = logging.getLogger("opensky.connector")


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", default=os.getenv("OPENSKY_ENV_FILE"))
    parser.add_argument("--once", action="store_true")
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)
    logging.basicConfig(level=os.getenv("LOG_LEVEL", "INFO").upper(), format="%(asctime)s %(levelname)s %(name)s: %(message)s")

    hub_config = HubConfig(
        http_url=require_env("HUB_HTTP_URL"),
        ws_url=require_env("HUB_WS_URL"),
        token=os.getenv("HUB_TOKEN") or None,
    )
    hub_rest = HubRESTClient(hub_config)
    hub_ws = HubWebSocketPublisher(hub_config)
    bbox = resolve_bbox()

    provider_id = os.getenv("OPENSKY_PROVIDER_ID", "opensky-demo")
    provider_type = os.getenv("OPENSKY_PROVIDER_TYPE", "adsb")
    provider_name = os.getenv("OPENSKY_PROVIDER_NAME", "OpenSky Aircraft Demonstrator")
    poll_interval = float(os.getenv("OPENSKY_POLL_INTERVAL_SECONDS", "20"))
    on_ground_only = (os.getenv("OPENSKY_ON_GROUND_ONLY") or "").strip().lower() in {"1", "true", "yes"}

    hub_rest.ensure_provider(
        provider_id=provider_id,
        provider_type=provider_type,
        name=provider_name,
        properties={"connector": "opensky", "bbox": bbox.__dict__},
    )

    known_trackables: set[str] = set()
    try:
        while True:
            try:
                payload = fetch_states(require_env("OPENSKY_URL"), bbox, timeout_seconds=hub_config.timeout_seconds)
                locations = build_locations(
                    payload.get("states") or [],
                    provider_id,
                    provider_type,
                    hub_rest,
                    known_trackables,
                    on_ground_only,
                )
                if locations:
                    LOGGER.info("publishing %d aircraft locations", len(locations))
                    hub_ws.publish_locations(locations)
                else:
                    LOGGER.info("no matching aircraft positions in current payload")
                if args.once:
                    return 0
            except KeyboardInterrupt:
                LOGGER.info("stopping connector")
                return 0
            except Exception:
                LOGGER.exception("poll iteration failed; retrying after %.1fs", poll_interval)
            time.sleep(poll_interval)
    finally:
        hub_ws.close()


def build_locations(
    states: list[list[object]],
    provider_id: str,
    provider_type: str,
    hub_rest: HubRESTClient,
    known_trackables: set[str],
    on_ground_only: bool,
) -> list[dict[str, object]]:
    locations: list[dict[str, object]] = []
    for row in states:
        if len(row) < 17:
            continue
        icao24 = (row[0] or "").strip()
        callsign = (row[1] or "").strip()
        origin_country = row[2]
        time_position = row[3]
        last_contact = row[4]
        longitude = row[5]
        latitude = row[6]
        baro_altitude = row[7]
        on_ground = bool(row[8])
        velocity = row[9]
        true_track = row[10]
        vertical_rate = row[11]
        geo_altitude = row[13] if len(row) > 13 else None
        squawk = row[14] if len(row) > 14 else None

        if not icao24 or latitude is None or longitude is None:
            continue
        if on_ground_only and not on_ground:
            continue

        trackable_id = deterministic_uuid("aircraft", icao24)
        if trackable_id not in known_trackables:
            hub_rest.ensure_trackable(
                trackable_id=trackable_id,
                name=callsign or icao24,
                provider_id=provider_id,
                properties={"connector": "opensky", "icao24": icao24, "callsign": callsign or None},
            )
            known_trackables.add(trackable_id)

        location: dict[str, object] = {
            "position": point(float(longitude), float(latitude)),
            "crs": "EPSG:4326",
            "provider_id": provider_id,
            "provider_type": provider_type,
            "source": f"opensky:{icao24}",
            "trackables": [trackable_id],
            "properties": {
                "connector": "opensky",
                "icao24": icao24,
                "callsign": callsign or None,
                "origin_country": origin_country,
                "on_ground": on_ground,
                "baro_altitude": baro_altitude,
                "geo_altitude": geo_altitude,
                "vertical_rate": vertical_rate,
                "squawk": squawk,
            },
        }
        if velocity is not None:
            location["speed"] = float(velocity)
        if true_track is not None:
            location["course"] = float(true_track)
        if time_position:
            location["timestamp_generated"] = format_unix_timestamp(int(time_position))
        elif last_contact:
            location["timestamp_generated"] = format_unix_timestamp(int(last_contact))
        locations.append(location)
    return locations


def format_unix_timestamp(timestamp: int) -> str:
    return datetime.fromtimestamp(timestamp, tz=timezone.utc).isoformat()


def require_env(name: str) -> str:
    value = os.getenv(name)
    if not value:
        raise SystemExit(f"{name} is required")
    return value


if __name__ == "__main__":
    raise SystemExit(main())
