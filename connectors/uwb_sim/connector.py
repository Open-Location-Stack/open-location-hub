#!/usr/bin/env python3
"""Mock multi-floor UWB simulator for the Open RTLS Hub demo stack."""

from __future__ import annotations

import argparse
import logging
import os
import time
from pathlib import Path

from assets import ensure_floorplan_assets
from hub_client import HubConfig, HubRESTClient, HubWebSocketPublisher, deterministic_uuid, point
from simulator import BuildingGraph, TRACKABLE_RADIUS_METERS, advance_agent, agent_location_payload, initial_agents
from uwb_support import build_floor_definitions, floor_zone_properties, load_env_file, now_utc, require_env


LOGGER = logging.getLogger("uwb_sim.connector")


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", default=os.getenv("UWB_SIM_ENV_FILE"))
    parser.add_argument(
        "--run-seconds",
        type=float,
        default=float(os.getenv("UWB_SIM_RUN_SECONDS", "0")),
        help="Optional simulation duration. 0 means run until interrupted.",
    )
    parser.add_argument(
        "--object-count",
        type=int,
        default=int(os.getenv("UWB_SIM_OBJECT_COUNT", "10")),
        help="Number of simulated trackables. Defaults to 10.",
    )
    parser.add_argument(
        "--ingest-rate-hz",
        type=float,
        default=float(os.getenv("UWB_SIM_INGEST_RATE_HZ", "25.0")),
        help="Raw ingest publish rate in Hertz. Defaults to 25.",
    )
    parser.add_argument(
        "--random-seed",
        type=int,
        default=int(os.getenv("UWB_SIM_RANDOM_SEED", "42")),
        help="Deterministic seed for reproducible movement.",
    )
    parser.add_argument(
        "--bootstrap-only",
        action=argparse.BooleanOptionalAction,
        default=False,
        help="Generate floorplan assets and bootstrap hub metadata without starting the publish loop.",
    )
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)

    if args.object_count <= 0:
        raise SystemExit("--object-count must be greater than 0")
    if args.ingest_rate_hz <= 0:
        raise SystemExit("--ingest-rate-hz must be greater than 0")
    if args.run_seconds < 0:
        raise SystemExit("--run-seconds must be greater than or equal to 0")

    logging.basicConfig(level=os.getenv("LOG_LEVEL", "INFO").upper(), format="%(asctime)s %(levelname)s %(name)s: %(message)s")

    provider_id = os.getenv("UWB_SIM_PROVIDER_ID", "uwb-sim-demo").strip()
    building_id = os.getenv("UWB_SIM_BUILDING_ID", "pacman-building").strip()
    connector_root = Path(__file__).resolve().parent
    asset_dir = connector_root / "assets"

    hub_config = HubConfig(
        http_url=(os.getenv("HUB_HTTP_URL") or "").strip() or None,
        ws_url=require_env("HUB_WS_URL"),
        token=os.getenv("HUB_TOKEN") or None,
    )
    floors = build_floor_definitions(building_id, asset_dir)
    created_assets = ensure_floorplan_assets(floors)
    LOGGER.info("generated %d floorplan assets", len(created_assets))

    hub_rest = HubRESTClient(hub_config)
    bootstrap_hub(hub_rest, provider_id, floors, args.object_count)
    if args.bootstrap_only:
        LOGGER.info("bootstrap-only mode complete")
        return 0

    trackables = build_trackables(provider_id, args.object_count)
    graph = BuildingGraph(floors)
    agents = initial_agents(graph, trackables, args.random_seed)
    publisher = HubWebSocketPublisher(hub_config)
    floors_by_number = {floor.floor_number: floor for floor in floors}

    tick_interval = 1.0 / args.ingest_rate_hz
    start_monotonic = time.monotonic()
    next_tick = start_monotonic
    emitted_ticks = 0

    try:
        while True:
            now_monotonic = time.monotonic()
            if args.run_seconds > 0 and (now_monotonic - start_monotonic) >= args.run_seconds:
                break
            if now_monotonic < next_tick:
                time.sleep(min(next_tick - now_monotonic, 0.01))
                continue

            elapsed_seconds = now_monotonic - start_monotonic
            timestamp = now_utc()
            batch = []
            for agent in agents:
                advance_agent(agent, graph, tick_interval, elapsed_seconds)
                batch.append(agent_location_payload(agent, graph, floors_by_number, provider_id, timestamp, elapsed_seconds))
            publisher.publish_locations(batch)
            emitted_ticks += 1
            next_tick += tick_interval
    except KeyboardInterrupt:
        LOGGER.info("stopping mock UWB simulator")
    finally:
        publisher.close()

    LOGGER.info("published %d batches at %.2f Hz for %d objects", emitted_ticks, args.ingest_rate_hz, len(agents))
    return 0


def bootstrap_hub(hub_rest: HubRESTClient, provider_id: str, floors, object_count: int) -> None:
    if not hub_rest.config.http_url:
        LOGGER.info("HUB_HTTP_URL not set; skipping provider, trackable, zone, and fence bootstrap")
        return

    hub_rest.ensure_provider(
        provider_id=provider_id,
        provider_type="uwb",
        name="Mock UWB simulator",
        properties={"connector": "uwb_sim"},
    )

    for trackable_id, name in build_trackables(provider_id, object_count):
        hub_rest.ensure_trackable(
            trackable_id=trackable_id,
            name=name,
            provider_id=provider_id,
            radius=TRACKABLE_RADIUS_METERS,
            properties={"connector": "uwb_sim"},
        )

    for floor in floors:
        hub_rest.ensure_zone(
            floor.zone_id,
            {
                "id": floor.zone_id,
                "type": "uwb",
                "foreign_id": f"{floor.building_id}-floor-{floor.floor_number}",
                "building": floor.building_id,
                "floor": float(floor.floor_number),
                "name": f"Pac-Man floor {floor.floor_number}",
                "description": "Mock multi-floor UWB building used by the connector demo",
                "position": point(*floor.center),
                "incomplete_configuration": True,
                "properties": floor_zone_properties(floor),
            },
        )
        hub_rest.ensure_fence(
            floor.fence_id,
            {
                "id": floor.fence_id,
                "foreign_id": f"{floor.building_id}-fence-{floor.floor_number}",
                "name": f"Pac-Man floor fence {floor.floor_number}",
                "crs": "local",
                "zone_id": floor.zone_id,
                "floor": float(floor.floor_number),
                "region": {"type": "Polygon", "coordinates": [floor.outline_ring]},
                "properties": {
                    "connector": "uwb_sim",
                    "building_id": floor.building_id,
                    "floorplan_id": floor.floorplan_id,
                    "zone_id": floor.zone_id,
                    "floorplan_corners_local": floor.image_corners_local,
                },
            },
        )


def build_trackables(provider_id: str, object_count: int) -> list[tuple[str, str]]:
    trackables: list[tuple[str, str]] = []
    for index in range(object_count):
        object_name = f"mock-uwb-{index + 1:02d}"
        trackables.append((deterministic_uuid("trackable", f"{provider_id}:{object_name}"), object_name))
    return trackables


if __name__ == "__main__":
    raise SystemExit(main())
