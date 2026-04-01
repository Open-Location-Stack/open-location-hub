"""Shared GTFS and station geometry helpers for the demonstrator scripts."""

from __future__ import annotations

import csv
import io
import logging
import math
import os
import tempfile
import zipfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Iterable

import requests
from google.transit import gtfs_realtime_pb2
from pyproj import Transformer


LOGGER = logging.getLogger(__name__)
IDFM_DATASET_BASE = "https://data.iledefrance-mobilites.fr/api/explore/v2.1/catalog/datasets"
LAMBERT93_TO_WGS84 = Transformer.from_crs("EPSG:2154", "EPSG:4326", always_xy=True)


def load_env_file(path: str | None) -> None:
    """Load KEY=VALUE pairs into os.environ when a local env file exists."""

    if not path:
        return
    env_path = Path(path)
    if not env_path.exists():
        return
    for line in env_path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or "=" not in stripped:
            continue
        key, value = stripped.split("=", 1)
        os.environ.setdefault(key.strip(), value.strip())


def truthy_env(name: str, default: bool = False) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


@dataclass
class StopRecord:
    stop_id: str
    stop_name: str
    stop_lat: float | None
    stop_lon: float | None
    parent_station: str | None
    location_type: str | None

    @property
    def coordinates(self) -> tuple[float, float] | None:
        if self.stop_lon is None or self.stop_lat is None:
            return None
        return (self.stop_lon, self.stop_lat)


@dataclass
class StationRecord:
    station_id: str
    name: str
    primary_stop: StopRecord | None
    child_stops: list[StopRecord] = field(default_factory=list)

    @property
    def numeric_id(self) -> str | None:
        return extract_idfm_numeric(self.station_id)

    @property
    def all_points(self) -> list[tuple[float, float]]:
        points: list[tuple[float, float]] = []
        if self.primary_stop and self.primary_stop.coordinates is not None:
            points.append(self.primary_stop.coordinates)
        for stop in self.child_stops:
            if stop.coordinates is not None:
                points.append(stop.coordinates)
        return unique_points(points)


@dataclass
class GTFSIndex:
    stops: dict[str, StopRecord]
    stations: dict[str, StationRecord]
    routes: dict[str, dict[str, str]]
    trips: dict[str, dict[str, str]]

    def station_for_stop(self, stop_id: str | None) -> StationRecord | None:
        if not stop_id:
            return None
        if stop_id in self.stations:
            return self.stations[stop_id]
        stop = self.stops.get(stop_id)
        if not stop:
            return None
        if stop.parent_station and stop.parent_station in self.stations:
            return self.stations[stop.parent_station]
        return None


@dataclass
class StationGeometry:
    station: StationRecord
    centroid: tuple[float, float]
    polygon_ring: list[list[float]]
    generation_mode: str
    source_point_count: int
    point_sources: list[str]


def ensure_local_file(source: str) -> Path:
    """Return a local path for either an existing file or a downloaded URL."""

    if source.startswith("http://") or source.startswith("https://"):
        response = requests.get(source, timeout=120)
        response.raise_for_status()
        suffix = Path(source).suffix or ".zip"
        handle = tempfile.NamedTemporaryFile(delete=False, suffix=suffix)
        handle.write(response.content)
        handle.flush()
        handle.close()
        LOGGER.info("downloaded %s to %s", source, handle.name)
        return Path(handle.name)
    return Path(source)


def load_gtfs_index(source: str) -> GTFSIndex:
    """Load a GTFS zip and build quick lookup tables for stops, trips, and routes."""

    zip_path = ensure_local_file(source)
    with zipfile.ZipFile(zip_path) as archive:
        stops = {
            row["stop_id"]: StopRecord(
                stop_id=row["stop_id"],
                stop_name=row.get("stop_name") or row["stop_id"],
                stop_lat=parse_float(row.get("stop_lat")),
                stop_lon=parse_float(row.get("stop_lon")),
                parent_station=row.get("parent_station") or None,
                location_type=row.get("location_type") or None,
            )
            for row in read_csv_from_zip(archive, "stops.txt")
        }
        routes = {
            row["route_id"]: row
            for row in read_csv_from_zip(archive, "routes.txt", required=False)
        }
        trips = {
            row["trip_id"]: row
            for row in read_csv_from_zip(archive, "trips.txt", required=False)
        }

    stations: dict[str, StationRecord] = {}
    for stop in stops.values():
        if stop.location_type == "1":
            stations[stop.stop_id] = StationRecord(
                station_id=stop.stop_id,
                name=stop.stop_name,
                primary_stop=stop,
            )

    for stop in stops.values():
        if stop.parent_station:
            station = stations.get(stop.parent_station)
            if station is None:
                station = StationRecord(
                    station_id=stop.parent_station,
                    name=stop.stop_name,
                    primary_stop=None,
                )
                stations[stop.parent_station] = station
            station.child_stops.append(stop)
            if station.primary_stop is None:
                station.primary_stop = stop

    for stop in stops.values():
        if stop.location_type == "1" or stop.parent_station:
            continue
        stations.setdefault(
            stop.stop_id,
            StationRecord(station_id=stop.stop_id, name=stop.stop_name, primary_stop=stop),
        )

    return GTFSIndex(stops=stops, stations=stations, routes=routes, trips=trips)


def decode_gtfs_rt_feed(payload: bytes) -> gtfs_realtime_pb2.FeedMessage:
    """Decode a GTFS-RT protobuf payload."""

    feed = gtfs_realtime_pb2.FeedMessage()
    feed.ParseFromString(payload)
    return feed


def fetch_gtfs_rt_feed(url: str, timeout_seconds: float = 30.0) -> gtfs_realtime_pb2.FeedMessage:
    """Download and decode the current GTFS-RT feed."""

    response = requests.get(url, timeout=timeout_seconds)
    response.raise_for_status()
    return decode_gtfs_rt_feed(response.content)


def build_station_geometries(
    gtfs: GTFSIndex,
    fallback_radius_meters: float,
    station_filter: str | None = None,
    max_stations: int | None = None,
) -> list[StationGeometry]:
    """Build station centroids and polygons from GTFS and optional references."""

    references = load_reference_datasets()
    results: list[StationGeometry] = []
    normalized_filter = station_filter.lower() if station_filter else None
    polygon_mode = (os.getenv("GTFS_STATION_POLYGON_MODE") or "circle").strip().lower()
    circle_radius = parse_float(os.getenv("GTFS_STATION_RADIUS_METERS")) or fallback_radius_meters
    hull_buffer_meters = parse_float(os.getenv("GTFS_STATION_HULL_BUFFER_METERS")) or 0.0

    for station in gtfs.stations.values():
        if normalized_filter and normalized_filter not in station.name.lower():
            continue
        geometry = build_station_geometry(
            station,
            references,
            fallback_radius_meters=circle_radius,
            polygon_mode=polygon_mode,
            hull_buffer_meters=hull_buffer_meters,
        )
        if geometry is None:
            continue
        results.append(geometry)
        if max_stations is not None and len(results) >= max_stations:
            break

    return results


def build_station_geometry(
    station: StationRecord,
    references: dict[str, Any],
    fallback_radius_meters: float,
    polygon_mode: str,
    hull_buffer_meters: float,
) -> StationGeometry | None:
    """Build one station polygon, falling back to a circle when needed."""

    points: list[tuple[float, float]] = []
    point_sources: list[str] = []
    numeric_id = station.numeric_id

    for point in station.all_points:
        points.append(point)
        point_sources.append("gtfs_stop")

    if numeric_id:
        for point in references["access_points"].get(numeric_id, []):
            points.append(point)
            point_sources.append("idfm_access")
        for point in references["stop_points"].get(numeric_id, []):
            points.append(point)
            point_sources.append("idfm_stop")
        centroid = references["zone_centroids"].get(numeric_id)
        if centroid is not None:
            points.append(centroid)
            point_sources.append("idfm_zone")

    points = unique_points(points)
    if not points:
        return None

    centroid = compute_centroid(points)
    if polygon_mode in {"auto", "hull"} and len(points) >= 3:
        hull = convex_hull(points)
        if len(hull) >= 3:
            ring = [[lon, lat] for lon, lat in hull]
            if hull_buffer_meters > 0:
                ring = expand_ring(ring, centroid, hull_buffer_meters)
            ring = close_ring(ring)
            return StationGeometry(
                station=station,
                centroid=centroid,
                polygon_ring=ring,
                generation_mode="convex_hull" if hull_buffer_meters <= 0 else "buffered_hull",
                source_point_count=len(points),
                point_sources=sorted(set(point_sources)),
            )

    ring = buffered_circle(centroid, fallback_radius_meters)
    return StationGeometry(
        station=station,
        centroid=centroid,
        polygon_ring=ring,
        generation_mode="buffered_circle",
        source_point_count=len(points),
        point_sources=sorted(set(point_sources)),
    )


def load_reference_datasets() -> dict[str, Any]:
    """Load optional external station reference datasets for polygon generation."""

    dataset_family = (os.getenv("GTFS_REFERENCE_DATASET_FAMILY") or "").strip().lower()
    if not dataset_family:
        return empty_reference_datasets()
    if dataset_family != "idfm":
        LOGGER.warning("unknown GTFS_REFERENCE_DATASET_FAMILY=%s; ignoring", dataset_family)
        return empty_reference_datasets()

    try:
        return load_idfm_references()
    except Exception:
        LOGGER.warning("failed to load IDFM reference datasets; falling back to GTFS-only geometry", exc_info=True)
        return empty_reference_datasets()


def empty_reference_datasets() -> dict[str, Any]:
    return {
        "access_points": {},
        "stop_points": {},
        "zone_centroids": {},
    }


def load_idfm_references() -> dict[str, Any]:
    """Fetch the IDFM stop-area datasets needed for station polygon generation."""

    access_records = fetch_dataset_records("acces")
    relation_records = fetch_dataset_records("relations-acces")
    stop_records = fetch_dataset_records("arrets")
    zone_records = fetch_dataset_records("zones-d-arrets")

    access_by_id: dict[str, tuple[float, float]] = {}
    for record in access_records:
        geopoint = record.get("accgeopoint") or {}
        lon = parse_float(geopoint.get("lon"))
        lat = parse_float(geopoint.get("lat"))
        if lon is None or lat is None:
            continue
        access_by_id[str(record["accid"])] = (lon, lat)

    access_points: dict[str, list[tuple[float, float]]] = {}
    for relation in relation_records:
        zdaid = str(relation.get("zdaid") or "")
        accid = str(relation.get("accid") or "")
        point = access_by_id.get(accid)
        if not zdaid or point is None:
            continue
        access_points.setdefault(zdaid, []).append(point)

    stop_points: dict[str, list[tuple[float, float]]] = {}
    for record in stop_records:
        zdaid = str(record.get("zdaid") or "")
        geopoint = record.get("arrgeopoint") or {}
        lon = parse_float(geopoint.get("lon"))
        lat = parse_float(geopoint.get("lat"))
        if not zdaid or lon is None or lat is None:
            continue
        stop_points.setdefault(zdaid, []).append((lon, lat))

    zone_centroids: dict[str, tuple[float, float]] = {}
    for record in zone_records:
        zdaid = str(record.get("zdaid") or "")
        x = parse_float(record.get("zdaxepsg2154"))
        y = parse_float(record.get("zdayepsg2154"))
        if not zdaid or x is None or y is None:
            continue
        lon, lat = LAMBERT93_TO_WGS84.transform(x, y)
        zone_centroids[zdaid] = (lon, lat)

    return {
        "access_points": access_points,
        "stop_points": stop_points,
        "zone_centroids": zone_centroids,
    }


def fetch_dataset_records(dataset: str, page_size: int = 100) -> list[dict[str, Any]]:
    """Fetch all records for one IDFM Opendatasoft dataset."""

    offset = 0
    results: list[dict[str, Any]] = []
    while True:
        response = requests.get(
            f"{IDFM_DATASET_BASE}/{dataset}/records",
            params={"limit": page_size, "offset": offset},
            timeout=60,
        )
        response.raise_for_status()
        payload = response.json()
        page = payload.get("results", [])
        results.extend(page)
        if len(page) < page_size:
            return results
        offset += page_size


def read_csv_from_zip(
    archive: zipfile.ZipFile,
    name: str,
    required: bool = True,
) -> Iterable[dict[str, str]]:
    """Yield CSV rows from one file inside a GTFS zip."""

    if name not in archive.namelist():
        if required:
            raise FileNotFoundError(f"{name} is required in the GTFS archive")
        return []
    with archive.open(name, "r") as handle:
        text = io.TextIOWrapper(handle, encoding="utf-8-sig", newline="")
        reader = csv.DictReader(text)
        return list(reader)


def extract_idfm_numeric(identifier: str | None) -> str | None:
    """Extract the final numeric token from IDFM stop identifiers."""

    if not identifier:
        return None
    token = identifier.rsplit(":", 1)[-1]
    return token if token.isdigit() else None


def parse_float(value: Any) -> float | None:
    if value in (None, ""):
        return None
    return float(value)


def unique_points(points: Iterable[tuple[float, float]]) -> list[tuple[float, float]]:
    seen: set[tuple[float, float]] = set()
    unique: list[tuple[float, float]] = []
    for lon, lat in points:
        key = (round(lon, 7), round(lat, 7))
        if key in seen:
            continue
        seen.add(key)
        unique.append((lon, lat))
    return unique


def compute_centroid(points: list[tuple[float, float]]) -> tuple[float, float]:
    count = float(len(points))
    lon = sum(point[0] for point in points) / count
    lat = sum(point[1] for point in points) / count
    return (lon, lat)


def cross(
    origin: tuple[float, float],
    a: tuple[float, float],
    b: tuple[float, float],
) -> float:
    return (a[0] - origin[0]) * (b[1] - origin[1]) - (a[1] - origin[1]) * (b[0] - origin[0])


def convex_hull(points: list[tuple[float, float]]) -> list[tuple[float, float]]:
    """Compute a monotonic-chain convex hull."""

    if len(points) <= 1:
        return points

    sorted_points = sorted(points)
    lower: list[tuple[float, float]] = []
    for point in sorted_points:
        while len(lower) >= 2 and cross(lower[-2], lower[-1], point) <= 0:
            lower.pop()
        lower.append(point)

    upper: list[tuple[float, float]] = []
    for point in reversed(sorted_points):
        while len(upper) >= 2 and cross(upper[-2], upper[-1], point) <= 0:
            upper.pop()
        upper.append(point)

    return lower[:-1] + upper[:-1]


def close_ring(ring: list[list[float]]) -> list[list[float]]:
    if not ring:
        return ring
    if ring[0] != ring[-1]:
        ring.append(list(ring[0]))
    return ring


def buffered_circle(
    center: tuple[float, float],
    radius_meters: float,
    steps: int = 24,
) -> list[list[float]]:
    """Approximate a meter buffer around a point as a GeoJSON polygon ring."""

    lon, lat = center
    lat_factor = radius_meters / 111_320.0
    lon_factor = radius_meters / (111_320.0 * max(math.cos(math.radians(lat)), 0.2))
    ring: list[list[float]] = []
    for index in range(steps):
        angle = (2.0 * math.pi * index) / steps
        ring.append(
            [
                lon + math.cos(angle) * lon_factor,
                lat + math.sin(angle) * lat_factor,
            ]
        )
    return close_ring(ring)


def expand_ring(
    ring: list[list[float]],
    centroid: tuple[float, float],
    buffer_meters: float,
) -> list[list[float]]:
    """Expand polygon vertices away from the centroid by a fixed distance."""

    expanded: list[list[float]] = []
    center_lon, center_lat = centroid
    lon_scale = 111_320.0 * max(math.cos(math.radians(center_lat)), 0.2)
    lat_scale = 111_320.0
    for lon, lat in ring:
        dx = (lon - center_lon) * lon_scale
        dy = (lat - center_lat) * lat_scale
        distance = math.hypot(dx, dy)
        if distance <= 1e-6:
            expanded.append([lon, lat])
            continue
        scale = (distance + buffer_meters) / distance
        expanded.append(
            [
                center_lon + (dx * scale) / lon_scale,
                center_lat + (dy * scale) / lat_scale,
            ]
        )
    return expanded
