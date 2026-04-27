"""Generate deterministic floorplan SVG assets for the mock UWB simulator."""

from __future__ import annotations

from pathlib import Path
from typing import Iterable

from uwb_support import FloorDefinition


FLOOR_FILL = "#f7f2e7"
FLOOR_STROKE = "#233b59"
CORRIDOR_STROKE = "#0b5fff"
CONNECTOR_FILL = "#f97316"
DEAD_END_FILL = "#dc2626"
NODE_FILL = "#111827"


def ensure_floorplan_assets(floors: Iterable[FloorDefinition]) -> list[str]:
    created: list[str] = []
    for floor in floors:
        path = Path(floor.image_path)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(render_floorplan_svg(floor), encoding="utf-8")
        created.append(str(path))
    return created


def render_floorplan_svg(floor: FloorDefinition) -> str:
    corners = floor.image_corners_local
    min_x = corners["top_left"][0]
    max_x = corners["top_right"][0]
    max_y = corners["top_left"][1]
    min_y = corners["bottom_left"][1]
    width = max_x - min_x
    height = max_y - min_y

    def project(point: tuple[float, float, float] | list[float]) -> tuple[float, float]:
        x = float(point[0])
        y = float(point[1])
        px = ((x - min_x) / width) * floor.image_width
        py = ((max_y - y) / height) * floor.image_height
        return round(px, 2), round(py, 2)

    polygon_points = " ".join(f"{x},{y}" for x, y in (project(point) for point in floor.outline_ring))
    edge_lines = []
    for left, right in floor.edges:
        x1, y1 = project(floor.nodes[left])
        x2, y2 = project(floor.nodes[right])
        edge_lines.append(
            f'<line x1="{x1}" y1="{y1}" x2="{x2}" y2="{y2}" stroke="{CORRIDOR_STROKE}" '
            'stroke-width="30" stroke-linecap="round" opacity="0.92" />'
        )

    node_markers = []
    for name, point in floor.nodes.items():
        cx, cy = project(point)
        fill = NODE_FILL
        radius = 8
        if name == "connector":
            fill = CONNECTOR_FILL
            radius = 12
        elif name.endswith("dead"):
            fill = DEAD_END_FILL
            radius = 10
        node_markers.append(f'<circle cx="{cx}" cy="{cy}" r="{radius}" fill="{fill}" />')

    label_x, label_y = project((corners["top_left"][0] + 2.0, corners["top_left"][1] - 2.0, floor.z_base))
    return "\n".join(
        [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{floor.image_width}" height="{floor.image_height}" viewBox="0 0 {floor.image_width} {floor.image_height}" role="img" aria-labelledby="title desc">',
            f'  <title id="title">Mock UWB simulator floor {floor.floor_number}</title>',
            f'  <desc id="desc">Pac-Man floorplan used by the Open RTLS Hub mock UWB simulator for floor {floor.floor_number}.</desc>',
            f'  <rect x="0" y="0" width="{floor.image_width}" height="{floor.image_height}" fill="#ffffff" />',
            f'  <polygon points="{polygon_points}" fill="{FLOOR_FILL}" stroke="{FLOOR_STROKE}" stroke-width="8" />',
            *[f"  {line}" for line in edge_lines],
            *[f"  {marker}" for marker in node_markers],
            f'  <text x="{label_x}" y="{label_y}" font-family="Menlo, Monaco, monospace" font-size="28" fill="{FLOOR_STROKE}">Floor {floor.floor_number}</text>',
            "</svg>",
            "",
        ]
    )
