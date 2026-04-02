#!/usr/bin/env python3
"""Compare logged locations against current hub fences and summarize proximity."""

from __future__ import annotations

import argparse
import json
import math
import os
import sys
from pathlib import Path
from typing import Any

import requests

SCRIPT_DIR = Path(__file__).resolve().parent
ROOT_DIR = SCRIPT_DIR.parent
sys.path.insert(0, str(ROOT_DIR))

from gtfs_support import load_env_file  # noqa: E402


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", default=os.getenv("GTFS_ENV_FILE"))
    parser.add_argument("--locations-log", default="connectors/gtfs/logs/location_updates.ndjson")
    parser.add_argument("--http-url", default=os.getenv("HUB_HTTP_URL"))
    parser.add_argument("--token", default=os.getenv("HUB_TOKEN"))
    return parser


def main() -> int:
    args = build_argument_parser().parse_args()
    load_env_file(args.env_file)

    http_url = args.http_url or os.getenv("HUB_HTTP_URL")
    if not http_url:
        raise SystemExit("HUB_HTTP_URL or --http-url is required")

    fences = fetch_fences(http_url, args.token or os.getenv("HUB_TOKEN"))
    fence_polygons = [normalize_fence(fence) for fence in fences if normalize_fence(fence) is not None]
    locations = load_locations(Path(args.locations_log))

    summary = summarize_proximity(locations, fence_polygons)
    print(json.dumps(summary, indent=2))
    return 0


def fetch_fences(http_url: str, token: str | None) -> list[dict[str, Any]]:
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    response = requests.get(f"{http_url.rstrip('/')}/v2/fences", headers=headers, timeout=30)
    response.raise_for_status()
    return response.json()


def normalize_fence(fence: dict[str, Any]) -> dict[str, Any] | None:
    region = fence.get("region") or {}
    if region.get("type") != "Polygon":
        return None
    coordinates = region.get("coordinates") or []
    if not coordinates or not coordinates[0]:
        return None
    return {
        "id": fence.get("id"),
        "foreign_id": fence.get("foreign_id"),
        "name": fence.get("name"),
        "ring": coordinates[0],
    }


def load_locations(path: Path) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    if not path.exists():
        return results
    with path.open("r", encoding="utf-8") as handle:
        for line in handle:
            if not line.strip():
                continue
            record = json.loads(line)
            message = record.get("message", {})
            if message.get("event") != "message":
                continue
            for payload in message.get("payload", []):
                position = payload.get("position") or {}
                coordinates = position.get("coordinates") or []
                if len(coordinates) < 2:
                    continue
                results.append(
                    {
                        "received_at": record.get("received_at"),
                        "provider_id": payload.get("provider_id"),
                        "trackables": payload.get("trackables", []),
                        "longitude": coordinates[0],
                        "latitude": coordinates[1],
                    }
                )
    return results


def summarize_proximity(
    locations: list[dict[str, Any]],
    fences: list[dict[str, Any]],
) -> dict[str, Any]:
    inside_count = 0
    min_distance = None
    closest_examples: list[dict[str, Any]] = []

    for location in locations:
        best = None
        point = (location["longitude"], location["latitude"])
        for fence in fences:
            if point_in_polygon(point, fence["ring"]):
                inside_count += 1
                best = {"distance_meters": 0.0, "fence": fence}
                break
            distance = polygon_distance_meters(point, fence["ring"])
            if best is None or distance < best["distance_meters"]:
                best = {"distance_meters": distance, "fence": fence}

        if best is None:
            continue
        distance = best["distance_meters"]
        if min_distance is None or distance < min_distance:
            min_distance = distance
        closest_examples.append(
            {
                "received_at": location["received_at"],
                "provider_id": location["provider_id"],
                "trackables": location["trackables"],
                "distance_meters": round(distance, 2),
                "fence_id": best["fence"]["id"],
                "fence_name": best["fence"]["name"],
                "fence_foreign_id": best["fence"]["foreign_id"],
            }
        )

    closest_examples.sort(key=lambda item: item["distance_meters"])
    return {
        "location_count": len(locations),
        "fence_count": len(fences),
        "locations_inside_any_fence": inside_count,
        "closest_distance_meters": None if min_distance is None else round(min_distance, 2),
        "closest_examples": closest_examples[:10],
    }


def point_in_polygon(point: tuple[float, float], ring: list[list[float]]) -> bool:
    x, y = point
    inside = False
    for index in range(len(ring) - 1):
        x1, y1 = ring[index]
        x2, y2 = ring[index + 1]
        intersects = ((y1 > y) != (y2 > y)) and (
            x < (x2 - x1) * (y - y1) / ((y2 - y1) or 1e-12) + x1
        )
        if intersects:
            inside = not inside
    return inside


def polygon_distance_meters(point: tuple[float, float], ring: list[list[float]]) -> float:
    best = math.inf
    for index in range(len(ring) - 1):
        best = min(best, segment_distance_meters(point, tuple(ring[index]), tuple(ring[index + 1])))
    return best


def segment_distance_meters(
    point: tuple[float, float],
    start: tuple[float, float],
    end: tuple[float, float],
) -> float:
    lon_scale = 111_320.0 * math.cos(math.radians(point[1]))
    lat_scale = 111_320.0

    px, py = point[0] * lon_scale, point[1] * lat_scale
    sx, sy = start[0] * lon_scale, start[1] * lat_scale
    ex, ey = end[0] * lon_scale, end[1] * lat_scale

    dx = ex - sx
    dy = ey - sy
    if dx == 0 and dy == 0:
        return math.hypot(px - sx, py - sy)

    t = ((px - sx) * dx + (py - sy) * dy) / (dx * dx + dy * dy)
    t = max(0.0, min(1.0, t))
    cx = sx + t * dx
    cy = sy + t * dy
    return math.hypot(px - cx, py - cy)


if __name__ == "__main__":
    raise SystemExit(main())
