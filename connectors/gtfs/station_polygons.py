#!/usr/bin/env python3
"""Create station zones and polygon fences for GTFS stations in the local hub."""

from __future__ import annotations

import argparse
import json
import logging
import os

from gtfs_support import build_station_geometries, load_env_file, load_gtfs_index
from hub_client import HubConfig, HubRESTClient, deterministic_uuid, point


LOGGER = logging.getLogger("gtfs.station_polygons")


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", default=os.getenv("GTFS_ENV_FILE"))
    parser.add_argument(
        "--preview-json",
        help="Optional path for writing the generated station geometry preview",
    )
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)

    logging.basicConfig(
        level=os.getenv("LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    gtfs_static_url = require_env("GTFS_STATIC_URL")
    fallback_radius = float(os.getenv("GTFS_FALLBACK_RADIUS_METERS", "75"))
    station_filter = os.getenv("GTFS_STATION_FILTER") or None
    max_stations = optional_int(os.getenv("GTFS_MAX_STATIONS"))

    hub = HubRESTClient(
        HubConfig(
            http_url=require_env("HUB_HTTP_URL"),
            ws_url=os.getenv("HUB_WS_URL", "ws://localhost:8080/v2/ws/socket"),
            token=os.getenv("HUB_TOKEN") or None,
        )
    )
    gtfs = load_gtfs_index(gtfs_static_url)
    geometries = build_station_geometries(
        gtfs=gtfs,
        fallback_radius_meters=fallback_radius,
        station_filter=station_filter,
        max_stations=max_stations,
    )
    LOGGER.info("built %d station geometries", len(geometries))

    if args.preview_json:
        preview = [
            {
                "station_id": item.station.station_id,
                "station_name": item.station.name,
                "centroid": item.centroid,
                "generation_mode": item.generation_mode,
                "source_point_count": item.source_point_count,
                "point_sources": item.point_sources,
            }
            for item in geometries
        ]
        with open(args.preview_json, "w", encoding="utf-8") as handle:
            json.dump(preview, handle, indent=2)

    created = 0
    for geometry in geometries:
        station = geometry.station
        zone_id = deterministic_uuid("zone", station.station_id)
        fence_id = deterministic_uuid("fence", station.station_id)

        zone_payload = {
            "id": zone_id,
            "type": "rfid",
            "foreign_id": station.station_id,
            "name": station.name,
            "description": "GTFS station zone used by the demonstrator connector",
            "position": point(geometry.centroid[0], geometry.centroid[1]),
            "radius": fallback_radius,
            "properties": {
                "connector": "gtfs",
                "station_id": station.station_id,
                "generation_mode": geometry.generation_mode,
                "source_point_count": geometry.source_point_count,
                "point_sources": geometry.point_sources,
            },
        }
        hub.ensure_zone(zone_id, zone_payload)

        fence_payload = {
            "id": fence_id,
            "crs": "EPSG:4326",
            "foreign_id": station.station_id,
            "name": station.name,
            "region": {"type": "Polygon", "coordinates": [geometry.polygon_ring]},
            "properties": {
                "connector": "gtfs",
                "station_id": station.station_id,
                "station_zone_id": zone_id,
                "generation_mode": geometry.generation_mode,
                "source_point_count": geometry.source_point_count,
                "point_sources": geometry.point_sources,
            },
        }
        hub.ensure_fence(fence_id, fence_payload)
        created += 1

    LOGGER.info("upserted %d station zones and fences", created)
    return 0


def optional_int(value: str | None) -> int | None:
    if not value:
        return None
    return int(value)


def require_env(name: str) -> str:
    value = os.getenv(name)
    if not value:
        raise SystemExit(f"{name} is required")
    return value


if __name__ == "__main__":
    raise SystemExit(main())
