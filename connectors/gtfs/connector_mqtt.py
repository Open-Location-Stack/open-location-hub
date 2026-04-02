#!/usr/bin/env python3
"""Forward GTFS-RT vehicle positions to a local Open RTLS Hub over MQTT."""

from __future__ import annotations

import argparse
import logging
import os
import time

from connector import build_locations, require_env
from gtfs_support import fetch_gtfs_rt_feed, load_env_file, load_gtfs_index
from hub_client import HubConfig, HubMQTTPublisher, HubRESTClient


LOGGER = logging.getLogger("gtfs.connector_mqtt")


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
        mqtt_broker_url=require_env("MQTT_BROKER_URL"),
        mqtt_client_id=os.getenv("GTFS_MQTT_CLIENT_ID") or None,
        mqtt_username=os.getenv("GTFS_MQTT_USERNAME") or None,
        mqtt_password=os.getenv("GTFS_MQTT_PASSWORD") or None,
        mqtt_keepalive_seconds=int(os.getenv("GTFS_MQTT_KEEPALIVE_SECONDS", "30")),
        mqtt_qos=int(os.getenv("GTFS_MQTT_QOS", "1")),
        token=os.getenv("HUB_TOKEN") or None,
    )
    hub_rest = HubRESTClient(hub_config)
    hub_mqtt = HubMQTTPublisher(hub_config)

    gtfs = load_gtfs_index(gtfs_static_url)
    LOGGER.info("loaded GTFS index with %d stations and %d trips", len(gtfs.stations), len(gtfs.trips))

    hub_rest.ensure_provider(
        provider_id=provider_id,
        provider_type=provider_type,
        name=provider_name,
        properties={"connector": "gtfs", "transport": "mqtt"},
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
                LOGGER.info("publishing %d location updates over MQTT", len(locations))
                hub_mqtt.publish_locations(provider_id, locations)
            else:
                LOGGER.info("no matching vehicle positions in current feed")

            if args.once:
                return 0
            time.sleep(poll_interval)
    finally:
        hub_mqtt.close()


if __name__ == "__main__":
    raise SystemExit(main())
