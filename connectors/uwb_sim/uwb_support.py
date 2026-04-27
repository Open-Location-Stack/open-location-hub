"""Shared helpers for the mock UWB simulator connector."""

from __future__ import annotations

import math
import os
import uuid
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Iterable

FLOOR_COUNT = 3
FLOOR_HEIGHT_METERS = 5.0
FLOOR_X_OFFSET_METERS = 2.5
FLOOR_Y_OFFSET_METERS = 1.5
PACMAN_CENTER = (30.0, 30.0)
PACMAN_RADIUS = 28.0
PACMAN_MOUTH_HALF_ANGLE_DEGREES = 28.0
IMAGE_SIZE = (1024, 1024)
IMAGE_MARGIN_METERS = 4.0
BUILDING_NAMESPACE = "uwb-sim"
ID_NAMESPACE = uuid.UUID("4c7dbff3-5292-4e9f-bf4b-cff6e322e6af")

BASE_NODES: dict[str, tuple[float, float]] = {
    "mouth_upper": (46.0, 38.0),
    "upper_arc_1": (40.0, 52.0),
    "upper_arc_2": (27.0, 58.0),
    "left_arc_1": (13.0, 50.0),
    "left_arc_2": (5.0, 35.0),
    "left_arc_3": (8.0, 19.0),
    "lower_arc_1": (20.0, 7.0),
    "lower_arc_2": (36.0, 8.0),
    "mouth_lower": (46.0, 24.0),
    "center": (28.0, 30.0),
    "left_dead": (14.0, 30.0),
    "top_dead": (28.0, 46.0),
    "bottom_dead": (28.0, 14.0),
    "connector": (40.0, 30.0),
    "right_dead": (50.0, 30.0),
}

BASE_EDGES: tuple[tuple[str, str], ...] = (
    ("mouth_upper", "upper_arc_1"),
    ("upper_arc_1", "upper_arc_2"),
    ("upper_arc_2", "left_arc_1"),
    ("left_arc_1", "left_arc_2"),
    ("left_arc_2", "left_arc_3"),
    ("left_arc_3", "lower_arc_1"),
    ("lower_arc_1", "lower_arc_2"),
    ("lower_arc_2", "mouth_lower"),
    ("center", "left_dead"),
    ("center", "top_dead"),
    ("center", "bottom_dead"),
    ("center", "connector"),
    ("connector", "mouth_upper"),
    ("connector", "mouth_lower"),
    ("connector", "right_dead"),
)


def deterministic_uuid(kind: str, external_id: str) -> str:
    return str(uuid.uuid5(ID_NAMESPACE, f"{kind}:{external_id}"))


def load_env_file(path: str | None) -> None:
    if not path or not os.path.exists(path):
        return
    with open(path, "r", encoding="utf-8") as handle:
        for line in handle:
            stripped = line.strip()
            if not stripped or stripped.startswith("#") or "=" not in stripped:
                continue
            key, value = stripped.split("=", 1)
            os.environ.setdefault(key.strip(), value.strip())


def require_env(name: str) -> str:
    value = os.getenv(name)
    if not value:
        raise SystemExit(f"{name} is required")
    return value


def now_utc() -> datetime:
    return datetime.now(UTC)


@dataclass(frozen=True)
class FloorDefinition:
    floor_number: int
    building_id: str
    zone_id: str
    fence_id: str
    floorplan_id: str
    z_base: float
    x_offset: float
    y_offset: float
    nodes: dict[str, tuple[float, float, float]]
    edges: tuple[tuple[str, str], ...]
    outline_ring: list[list[float]]
    image_path: str
    image_width: int
    image_height: int
    image_corners_local: dict[str, list[float]]

    @property
    def center(self) -> tuple[float, float, float]:
        x, y = translate_point(PACMAN_CENTER[0], PACMAN_CENTER[1], self.x_offset, self.y_offset)
        return x, y, self.z_base


def build_floor_definitions(building_id: str, asset_dir: Path) -> list[FloorDefinition]:
    definitions: list[FloorDefinition] = []
    for floor_index in range(FLOOR_COUNT):
        floor_number = floor_index + 1
        x_offset = floor_index * FLOOR_X_OFFSET_METERS
        y_offset = floor_index * FLOOR_Y_OFFSET_METERS
        z_base = floor_index * FLOOR_HEIGHT_METERS
        zone_id = deterministic_uuid("zone", f"{building_id}:floor:{floor_number}")
        fence_id = deterministic_uuid("fence", f"{building_id}:floor:{floor_number}")
        floorplan_id = deterministic_uuid("floorplan", f"{building_id}:floor:{floor_number}")
        nodes = {
            name: (*translate_point(x, y, x_offset, y_offset), z_base)
            for name, (x, y) in BASE_NODES.items()
        }
        outline_ring = translated_outline_ring(x_offset, y_offset)
        corners = local_image_corners(outline_ring)
        definitions.append(
            FloorDefinition(
                floor_number=floor_number,
                building_id=building_id,
                zone_id=zone_id,
                fence_id=fence_id,
                floorplan_id=floorplan_id,
                z_base=z_base,
                x_offset=x_offset,
                y_offset=y_offset,
                nodes=nodes,
                edges=BASE_EDGES,
                outline_ring=outline_ring,
                image_path=str(asset_dir / f"floor-{floor_number}.svg"),
                image_width=IMAGE_SIZE[0],
                image_height=IMAGE_SIZE[1],
                image_corners_local=corners,
            )
        )
    return definitions


def translate_point(x: float, y: float, x_offset: float, y_offset: float) -> tuple[float, float]:
    return x + x_offset, y + y_offset


def translated_outline_ring(x_offset: float, y_offset: float) -> list[list[float]]:
    center_x, center_y = translate_point(PACMAN_CENTER[0], PACMAN_CENTER[1], x_offset, y_offset)
    radius = PACMAN_RADIUS
    start_degrees = PACMAN_MOUTH_HALF_ANGLE_DEGREES
    end_degrees = 360.0 - PACMAN_MOUTH_HALF_ANGLE_DEGREES
    tip_x = center_x + radius * 0.48
    tip_y = center_y
    ring: list[list[float]] = []
    for degree in range(int(start_degrees), int(end_degrees) + 1, 8):
        radians = math.radians(degree)
        x = center_x + math.cos(radians) * radius
        y = center_y + math.sin(radians) * radius
        ring.append([round(x, 3), round(y, 3)])
    ring.append([round(tip_x, 3), round(tip_y, 3)])
    ring.append(ring[0])
    return ring


def local_image_corners(outline_ring: Iterable[Iterable[float]]) -> dict[str, list[float]]:
    xs = [point[0] for point in outline_ring]
    ys = [point[1] for point in outline_ring]
    min_x = min(xs) - IMAGE_MARGIN_METERS
    max_x = max(xs) + IMAGE_MARGIN_METERS
    min_y = min(ys) - IMAGE_MARGIN_METERS
    max_y = max(ys) + IMAGE_MARGIN_METERS
    return {
        "top_left": [round(min_x, 3), round(max_y, 3)],
        "top_right": [round(max_x, 3), round(max_y, 3)],
        "bottom_right": [round(max_x, 3), round(min_y, 3)],
        "bottom_left": [round(min_x, 3), round(min_y, 3)],
    }


def floor_zone_properties(floor: FloorDefinition) -> dict[str, object]:
    corners = floor.image_corners_local
    return {
        "connector": BUILDING_NAMESPACE,
        "building_id": floor.building_id,
        "floorplan_id": floor.floorplan_id,
        "floorplan_image_path": relative_asset_path(floor.image_path),
        "floorplan_image_size": {"width": floor.image_width, "height": floor.image_height},
        "floorplan_corner_order": ["top_left", "top_right", "bottom_right", "bottom_left"],
        "floorplan_corners_local": corners,
        "floor_outline_local": floor.outline_ring,
        "floor_origin_local": [round(floor.x_offset, 3), round(floor.y_offset, 3), round(floor.z_base, 3)],
        "interfloor_connector_local": [round(floor.nodes["connector"][0], 3), round(floor.nodes["connector"][1], 3), round(floor.nodes["connector"][2], 3)],
    }


def relative_asset_path(asset_path: str) -> str:
    path = Path(asset_path)
    repo_root = Path(__file__).resolve().parents[2]
    resolved = path.resolve()
    try:
        return str(resolved.relative_to(repo_root))
    except ValueError:
        return str(resolved)
