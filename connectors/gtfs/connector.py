#!/usr/bin/env python3
"""Forward GTFS-RT vehicle positions to a local Open RTLS Hub over WebSocket."""

from __future__ import annotations

import argparse
import logging
import os
import time
from datetime import datetime, timezone
from typing import Any

from google.transit import gtfs_realtime_pb2

from gtfs_support import fetch_gtfs_rt_feed, load_env_file, load_gtfs_index
from hub_client import HubConfig, HubRESTClient, HubWebSocketPublisher, deterministic_uuid, point


LOGGER = logging.getLogger("gtfs.connector")


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", default=os.getenv("GTFS_ENV_FILE"))
    parser.add_argument("--once", action="store_true", help="Process one GTFS-RT fetch and exit")
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)

    logging.basicConfig(
        level=os.getenv("LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )

    gtfs_static_url = require_env("GTFS_STATIC_URL")
    gtfs_rt_url = require_env("GTFS_RT_URL")
    provider_id = os.getenv("GTFS_PROVIDER_ID", "gtfs-demo")
    provider_type = os.getenv("GTFS_PROVIDER_TYPE", "gtfs-rt")
    provider_name = os.getenv("GTFS_PROVIDER_NAME", "GTFS Demonstrator")
    route_filter = os.getenv("GTFS_ROUTE_FILTER") or None
    poll_interval = float(os.getenv("GTFS_POLL_INTERVAL_SECONDS", "15"))

    hub_config = HubConfig(
        http_url=require_env("HUB_HTTP_URL"),
        ws_url=require_env("HUB_WS_URL"),
        token=os.getenv("HUB_TOKEN") or None,
    )
    hub_rest = HubRESTClient(hub_config)
    hub_ws = HubWebSocketPublisher(hub_config)

    gtfs = load_gtfs_index(gtfs_static_url)
    LOGGER.info("loaded GTFS index with %d stations and %d trips", len(gtfs.stations), len(gtfs.trips))

    hub_rest.ensure_provider(
        provider_id=provider_id,
        provider_type=provider_type,
        name=provider_name,
        properties={"connector": "gtfs", "transport": "websocket"},
    )

    known_trackables: set[str] = set()
    try:
        while True:
            feed = fetch_gtfs_rt_feed(gtfs_rt_url)
            locations = build_locations(
                feed=feed,
                gtfs=gtfs,
                provider_id=provider_id,
                provider_type=provider_type,
                route_filter=route_filter,
                hub_rest=hub_rest,
                known_trackables=known_trackables,
            )
            if locations:
                LOGGER.info("publishing %d location updates", len(locations))
                hub_ws.publish_locations(locations)
            else:
                LOGGER.info("no matching vehicle positions in current feed")

            if args.once:
                return 0
            time.sleep(poll_interval)
    finally:
        hub_ws.close()


def build_locations(
    feed: gtfs_realtime_pb2.FeedMessage,
    gtfs: Any,
    provider_id: str,
    provider_type: str,
    route_filter: str | None,
    hub_rest: HubRESTClient,
    known_trackables: set[str],
) -> list[dict[str, Any]]:
    locations: list[dict[str, Any]] = []
    for entity in feed.entity:
        if not entity.HasField("vehicle"):
            continue
        vehicle = entity.vehicle
        if not vehicle.HasField("position"):
            continue

        trip = gtfs.trips.get(vehicle.trip.trip_id, {})
        route_id = trip.get("route_id") or vehicle.trip.route_id or None
        if route_filter and route_id != route_filter:
            continue

        vehicle_id = vehicle_identifier(entity.id, vehicle)
        trackable_id = None
        if vehicle_id:
            trackable_id = deterministic_uuid("trackable", vehicle_id)
            if trackable_id not in known_trackables:
                hub_rest.ensure_trackable(
                    trackable_id=trackable_id,
                    name=trackable_name(vehicle_id, trip, gtfs),
                    provider_id=provider_id,
                    properties=trackable_properties(vehicle_id, vehicle, trip),
                )
                known_trackables.add(trackable_id)

        location = build_location_payload(
            entity_id=entity.id,
            vehicle=vehicle,
            gtfs=gtfs,
            provider_id=provider_id,
            provider_type=provider_type,
            route_id=route_id,
            trip=trip,
            trackable_id=trackable_id,
            vehicle_id=vehicle_id,
        )
        if location is not None:
            locations.append(location)
    return locations


def build_location_payload(
    entity_id: str,
    vehicle: gtfs_realtime_pb2.VehiclePosition,
    gtfs: Any,
    provider_id: str,
    provider_type: str,
    route_id: str | None,
    trip: dict[str, str],
    trackable_id: str | None,
    vehicle_id: str | None,
) -> dict[str, Any] | None:
    latitude = vehicle.position.latitude
    longitude = vehicle.position.longitude
    if latitude == 0 and longitude == 0:
        return None

    station = gtfs.station_for_stop(vehicle.stop_id)
    route = gtfs.routes.get(route_id or "", {})

    payload: dict[str, Any] = {
        "position": point(longitude, latitude),
        "crs": "EPSG:4326",
        "provider_id": provider_id,
        "provider_type": provider_type,
        "source": source_identifier(vehicle.stop_id, vehicle_id, entity_id),
        "properties": {
            "connector": "gtfs",
            "entity_id": entity_id,
            "trip_id": vehicle.trip.trip_id or None,
            "route_id": route_id,
            "route_short_name": route.get("route_short_name"),
            "route_long_name": route.get("route_long_name"),
            "direction_id": trip.get("direction_id"),
            "vehicle_id": vehicle_id,
            "vehicle_label": vehicle.vehicle.label or None,
            "vehicle_license_plate": vehicle.vehicle.license_plate or None,
            "current_stop_sequence": vehicle.current_stop_sequence or None,
            "current_status": vehicle_status_name(vehicle.current_status),
            "stop_id": vehicle.stop_id or None,
            "station_id": station.station_id if station else None,
            "station_name": station.name if station else None,
            "bearing": vehicle.position.bearing or None,
            "speed": vehicle.position.speed or None,
        },
    }
    if vehicle.timestamp:
        payload["timestamp_generated"] = format_unix_timestamp(vehicle.timestamp)
    if vehicle.position.speed:
        payload["speed"] = vehicle.position.speed
    if vehicle.position.bearing:
        payload["course"] = vehicle.position.bearing
    if trackable_id:
        payload["trackables"] = [trackable_id]
    return payload


def trackable_name(vehicle_id: str, trip: dict[str, str], gtfs: Any) -> str:
    route = gtfs.routes.get(trip.get("route_id", ""), {})
    route_name = route.get("route_short_name") or route.get("route_long_name")
    if route_name:
        return f"{route_name} {vehicle_id}"
    return f"Vehicle {vehicle_id}"


def trackable_properties(
    vehicle_id: str,
    vehicle: gtfs_realtime_pb2.VehiclePosition,
    trip: dict[str, str],
) -> dict[str, Any]:
    return {
        "connector": "gtfs",
        "external_vehicle_id": vehicle_id,
        "trip_id": vehicle.trip.trip_id or None,
        "route_id": trip.get("route_id") or vehicle.trip.route_id or None,
        "vehicle_label": vehicle.vehicle.label or None,
    }


def vehicle_identifier(
    entity_id: str,
    vehicle: gtfs_realtime_pb2.VehiclePosition,
) -> str | None:
    for value in (vehicle.vehicle.id, vehicle.vehicle.label, entity_id):
        if value:
            return value
    return None


def vehicle_status_name(status: int) -> str:
    return gtfs_realtime_pb2.VehiclePosition.VehicleStopStatus.Name(status)


def source_identifier(stop_id: str, vehicle_id: str | None, entity_id: str) -> str:
    if stop_id:
        return f"gtfs-stop:{stop_id}"
    if vehicle_id:
        return f"gtfs-vehicle:{vehicle_id}"
    return f"gtfs-entity:{entity_id}"


def format_unix_timestamp(timestamp: int) -> str:
    return datetime.fromtimestamp(timestamp, tz=timezone.utc).isoformat()


def require_env(name: str) -> str:
    value = os.getenv(name)
    if not value:
        raise SystemExit(f"{name} is required")
    return value


if __name__ == "__main__":
    raise SystemExit(main())
