"""OpenSky polling and airport preset helpers."""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Any

import requests


def load_env_file(path: str | None) -> None:
    if not path:
        return
    if not os.path.exists(path):
        return
    with open(path, "r", encoding="utf-8") as handle:
        for line in handle:
            stripped = line.strip()
            if not stripped or stripped.startswith("#") or "=" not in stripped:
                continue
            key, value = stripped.split("=", 1)
            os.environ.setdefault(key.strip(), value.strip())


@dataclass(frozen=True)
class AirportArea:
    foreign_id: str
    name: str
    latitude: float
    longitude: float
    radius_meters: float
    description: str


@dataclass(frozen=True)
class BoundingBox:
    lamin: float
    lomin: float
    lamax: float
    lomax: float


AIRPORT_PRESETS: dict[str, list[AirportArea]] = {
    "frankfurt": [
        AirportArea("fra-airport", "Frankfurt Airport", 50.0379, 8.5622, 2200, "Airport-wide catchment"),
        AirportArea("fra-terminal-1", "Frankfurt Terminal 1", 50.0370, 8.5625, 500, "Terminal 1 sector"),
        AirportArea("fra-terminal-2", "Frankfurt Terminal 2", 50.0518, 8.5880, 500, "Terminal 2 sector"),
        AirportArea("fra-cargo-south", "Frankfurt Cargo South", 50.0260, 8.5705, 700, "Cargo and south apron sector"),
    ],
    "munich": [
        AirportArea("muc-airport", "Munich Airport", 48.3538, 11.7861, 2200, "Airport-wide catchment"),
        AirportArea("muc-terminal-1", "Munich Terminal 1", 48.3541, 11.7837, 450, "Terminal 1 sector"),
        AirportArea("muc-terminal-2", "Munich Terminal 2", 48.3535, 11.7755, 450, "Terminal 2 sector"),
        AirportArea("muc-satellite", "Munich Satellite Apron", 48.3530, 11.7710, 450, "Satellite and apron sector"),
    ],
    "germany": [
        AirportArea("fra-airport", "Frankfurt Airport", 50.0379, 8.5622, 2200, "Frankfurt airport catchment"),
        AirportArea("muc-airport", "Munich Airport", 48.3538, 11.7861, 2200, "Munich airport catchment"),
        AirportArea("ber-airport", "Berlin Brandenburg Airport", 52.3667, 13.5033, 2200, "Berlin Brandenburg airport catchment"),
        AirportArea("ham-airport", "Hamburg Airport", 53.6304, 9.9882, 1800, "Hamburg airport catchment"),
        AirportArea("dus-airport", "Dusseldorf Airport", 51.2895, 6.7668, 1800, "Dusseldorf airport catchment"),
    ],
    "newyork": [
        AirportArea("jfk-airport", "John F. Kennedy International Airport", 40.6413, -73.7781, 2400, "JFK airport catchment"),
        AirportArea("lga-airport", "LaGuardia Airport", 40.7769, -73.8740, 1800, "LaGuardia airport catchment"),
        AirportArea("ewr-airport", "Newark Liberty International Airport", 40.6895, -74.1745, 2200, "Newark airport catchment"),
        AirportArea("jfk-terminals", "JFK Terminal Core", 40.6447, -73.7827, 850, "JFK terminal and apron sector"),
        AirportArea("ewr-terminals", "Newark Terminal Core", 40.6899, -74.1770, 850, "Newark terminal and apron sector"),
        AirportArea("lga-terminals", "LaGuardia Terminal Core", 40.7729, -73.8705, 700, "LaGuardia terminal and apron sector"),
    ],
}


REGION_PRESETS: dict[str, BoundingBox] = {
    "frankfurt": BoundingBox(49.8, 8.1, 50.4, 8.95),
    "munich": BoundingBox(48.0, 11.2, 48.7, 12.1),
    "germany": BoundingBox(47.0, 5.5, 55.2, 15.8),
    "newyork": BoundingBox(40.15, -74.7, 41.1, -73.3),
}


def resolve_bbox() -> BoundingBox:
    preset = (os.getenv("OPENSKY_REGION_PRESET") or "frankfurt").strip().lower()
    explicit = [os.getenv("OPENSKY_LAMIN"), os.getenv("OPENSKY_LOMIN"), os.getenv("OPENSKY_LAMAX"), os.getenv("OPENSKY_LOMAX")]
    if all(explicit):
        return BoundingBox(*(float(value) for value in explicit))
    if preset not in REGION_PRESETS:
        raise SystemExit(f"unknown OPENSKY_REGION_PRESET={preset}")
    return REGION_PRESETS[preset]


def airport_areas() -> list[AirportArea]:
    preset = (os.getenv("OPENSKY_AIRPORT_PRESET") or "frankfurt").strip().lower()
    if preset not in AIRPORT_PRESETS:
        raise SystemExit(f"unknown OPENSKY_AIRPORT_PRESET={preset}")
    scale = float(os.getenv("OPENSKY_FENCE_RADIUS_SCALE", "1.0"))
    areas = []
    for area in AIRPORT_PRESETS[preset]:
        areas.append(
            AirportArea(
                foreign_id=area.foreign_id,
                name=area.name,
                latitude=area.latitude,
                longitude=area.longitude,
                radius_meters=area.radius_meters * scale,
                description=area.description,
            )
        )
    return areas


def fetch_states(url: str, bbox: BoundingBox, timeout_seconds: float = 30.0) -> dict[str, Any]:
    response = requests.get(
        url,
        params={
            "lamin": bbox.lamin,
            "lomin": bbox.lomin,
            "lamax": bbox.lamax,
            "lomax": bbox.lomax,
        },
        timeout=timeout_seconds,
    )
    response.raise_for_status()
    return response.json()
