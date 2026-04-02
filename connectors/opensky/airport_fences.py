#!/usr/bin/env python3
"""Create airport and apron sector zones/fences for the OpenSky demo."""

from __future__ import annotations

import argparse
import logging
import os

from hub_client import HubConfig, HubRESTClient, deterministic_uuid, point
from opensky_support import airport_areas, load_env_file


LOGGER = logging.getLogger("opensky.airport_fences")


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", default=os.getenv("OPENSKY_ENV_FILE"))
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)
    logging.basicConfig(level=os.getenv("LOG_LEVEL", "INFO").upper(), format="%(asctime)s %(levelname)s %(name)s: %(message)s")

    hub = HubRESTClient(
        HubConfig(
            http_url=require_env("HUB_HTTP_URL"),
            ws_url=require_env("HUB_WS_URL"),
            token=os.getenv("HUB_TOKEN") or None,
        )
    )

    upserted = 0
    for area in airport_areas():
        zone_id = deterministic_uuid("zone", area.foreign_id)
        fence_id = deterministic_uuid("fence", area.foreign_id)
        zone_payload = {
            "id": zone_id,
            "type": "rfid",
            "foreign_id": area.foreign_id,
            "name": area.name,
            "description": area.description,
            "position": point(area.longitude, area.latitude),
            "radius": area.radius_meters,
            "properties": {"connector": "opensky", "area_type": "airport_sector"},
        }
        fence_payload = {
            "id": fence_id,
            "crs": "EPSG:4326",
            "foreign_id": area.foreign_id,
            "name": area.name,
            "region": point(area.longitude, area.latitude),
            "radius": area.radius_meters,
            "properties": {"connector": "opensky", "area_type": "airport_sector", "zone_id": zone_id},
        }
        hub.ensure_zone(zone_id, zone_payload)
        hub.ensure_fence(fence_id, fence_payload)
        upserted += 1

    LOGGER.info("upserted %d airport sectors", upserted)
    return 0


def require_env(name: str) -> str:
    value = os.getenv(name)
    if not value:
        raise SystemExit(f"{name} is required")
    return value


if __name__ == "__main__":
    raise SystemExit(main())
