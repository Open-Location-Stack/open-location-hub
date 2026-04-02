#!/usr/bin/env python3
"""Compare logged locations against current hub fences and summarize proximity."""

from __future__ import annotations

import argparse
import json
import math
import os
from pathlib import Path
from typing import Any

import requests


def build_argument_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", help="Optional dotenv-style file loaded before resolving HUB_* settings")
    parser.add_argument("--locations-log", default="logs/location_updates.ndjson")
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
    normalized = [item for item in (normalize_fence(fence) for fence in fences) if item is not None]
    locations = load_locations(Path(args.locations_log))

    print(json.dumps(summarize_proximity(locations, normalized), indent=2))
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


def fetch_fences(http_url: str, token: str | None) -> list[dict[str, Any]]:
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    response = requests.get(f"{http_url.rstrip('/')}/v2/fences", headers=headers, timeout=30)
    response.raise_for_status()
    return response.json()


def normalize_fence(fence: dict[str, Any]) -> dict[str, Any] | None:
    region = fence.get("region") or {}
    if region.get("type") == "Point":
        coordinates = region.get("coordinates") or []
        if len(coordinates) < 2:
            return None
        return {
            "id": fence.get("id"),
            "foreign_id": fence.get("foreign_id"),
            "name": fence.get("name"),
            "kind": "circle",
            "center": (coordinates[0], coordinates[1]),
            "radius": float(fence.get("radius") or 0.0),
        }
    if region.get("type") == "Polygon":
        coordinates = region.get("coordinates") or []
        if not coordinates or not coordinates[0]:
            return None
        return {
            "id": fence.get("id"),
            "foreign_id": fence.get("foreign_id"),
            "name": fence.get("name"),
            "kind": "polygon",
            "ring": coordinates[0],
        }
    return None


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


def summarize_proximity(locations: list[dict[str, Any]], fences: list[dict[str, Any]]) -> dict[str, Any]:
    inside_count = 0
    min_distance = None
    closest_examples: list[dict[str, Any]] = []
    for location in locations:
        point = (location["longitude"], location["latitude"])
        best = None
        for fence in fences:
            if fence["kind"] == "circle":
                distance = max(haversine_meters(point, fence["center"]) - fence["radius"], 0.0)
                inside = distance == 0.0
            else:
                inside = point_in_polygon(point, fence["ring"])
                distance = 0.0 if inside else polygon_distance_meters(point, fence["ring"])
            if inside:
                inside_count += 1
                best = {"distance_meters": 0.0, "fence": fence}
                break
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


def haversine_meters(a: tuple[float, float], b: tuple[float, float]) -> float:
    lon1, lat1 = map(math.radians, a)
    lon2, lat2 = map(math.radians, b)
    dlon = lon2 - lon1
    dlat = lat2 - lat1
    value = math.sin(dlat / 2.0) ** 2 + math.cos(lat1) * math.cos(lat2) * math.sin(dlon / 2.0) ** 2
    return 6371000.0 * 2.0 * math.asin(math.sqrt(value))


def point_in_polygon(point: tuple[float, float], ring: list[list[float]]) -> bool:
    x, y = point
    inside = False
    for index in range(len(ring) - 1):
        x1, y1 = ring[index]
        x2, y2 = ring[index + 1]
        intersects = ((y1 > y) != (y2 > y)) and (x < (x2 - x1) * (y - y1) / ((y2 - y1) or 1e-12) + x1)
        if intersects:
            inside = not inside
    return inside


def polygon_distance_meters(point: tuple[float, float], ring: list[list[float]]) -> float:
    best = math.inf
    for index in range(len(ring) - 1):
        best = min(best, segment_distance_meters(point, tuple(ring[index]), tuple(ring[index + 1])))
    return best


def segment_distance_meters(point: tuple[float, float], start: tuple[float, float], end: tuple[float, float]) -> float:
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
