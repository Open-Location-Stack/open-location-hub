from __future__ import annotations

import tempfile
import unittest
from datetime import UTC, datetime
from pathlib import Path

from assets import ensure_floorplan_assets
from simulator import BuildingGraph, TRACKABLE_RADIUS_METERS, agent_location_payload, choose_next_node, current_speed, initial_agents
from uwb_support import build_floor_definitions, deterministic_uuid, floor_zone_properties


class FakeRandom:
    def __init__(self, roll: float, choice_index: int = 0):
        self.roll = roll
        self.choice_index = choice_index

    def random(self) -> float:
        return self.roll

    def choice(self, values):
        return values[self.choice_index % len(values)]


class UwbSimTests(unittest.TestCase):
    def test_floorplan_ids_are_deterministic(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            left = build_floor_definitions("building-a", Path(temp_dir) / "assets")
            right = build_floor_definitions("building-a", Path(temp_dir) / "assets")
        self.assertEqual([floor.floorplan_id for floor in left], [floor.floorplan_id for floor in right])
        self.assertEqual(left[0].floorplan_id, deterministic_uuid("floorplan", "building-a:floor:1"))

    def test_asset_generation_writes_svg_files(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            floors = build_floor_definitions("building-a", Path(temp_dir) / "assets")
            paths = ensure_floorplan_assets(floors)
            self.assertEqual(len(paths), 3)
            for path in paths:
                content = Path(path).read_text(encoding="utf-8")
                self.assertIn("<svg", content)
                self.assertIn("Pac-Man floorplan", content)

    def test_zone_properties_publish_floorplan_corner_coordinates(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            floor = build_floor_definitions("building-a", Path(temp_dir) / "assets")[0]
        props = floor_zone_properties(floor)
        self.assertEqual(props["floorplan_corner_order"], ["top_left", "top_right", "bottom_right", "bottom_left"])
        local_corners = props["floorplan_corners_local"]
        wgs84_corners = props["floorplan_corners_wgs84"]
        self.assertIn("top_left", local_corners)
        self.assertIn("bottom_right", local_corners)
        self.assertIn("top_left", wgs84_corners)
        self.assertIn("bottom_right", wgs84_corners)
        self.assertGreater(local_corners["top_right"][0], local_corners["top_left"][0])
        self.assertGreater(wgs84_corners["top_right"][0], wgs84_corners["top_left"][0])
        self.assertEqual(len(props["floor_outline_wgs84"]), len(props["floor_outline_local"]))

    def test_dead_end_reverses_direction(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            graph = BuildingGraph(build_floor_definitions("building-a", Path(temp_dir) / "assets"))
        current = graph.node_id(1, "left_dead")
        previous = graph.node_id(1, "center")
        next_node = choose_next_node(graph, current, previous, FakeRandom(roll=0.75))
        self.assertEqual(next_node, previous)

    def test_connector_node_can_switch_floors(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            graph = BuildingGraph(build_floor_definitions("building-a", Path(temp_dir) / "assets"))
        current = graph.node_id(1, "connector")
        previous = graph.node_id(1, "center")
        next_node = choose_next_node(graph, current, previous, FakeRandom(roll=0.2))
        self.assertEqual(graph.nodes[next_node].floor_number, 2)

    def test_generated_agents_use_half_meter_collision_radius_contract(self) -> None:
        self.assertEqual(TRACKABLE_RADIUS_METERS, 0.5)

    def test_speed_variation_stays_positive(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            graph = BuildingGraph(build_floor_definitions("building-a", Path(temp_dir) / "assets"))
        agents = initial_agents(graph, [("track-1", "track-1")], 42)
        self.assertGreater(current_speed(agents[0], 0.0), 0.0)
        self.assertGreater(current_speed(agents[0], 30.0), 0.0)

    def test_floor_definitions_include_ground_control_points(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            floor = build_floor_definitions("building-a", Path(temp_dir) / "assets")[0]
        self.assertEqual(len(floor.ground_control_points), 3)
        self.assertIn("local", floor.ground_control_points[0])
        self.assertIn("wgs84", floor.ground_control_points[0])

    def test_agent_payload_emits_wgs84_locations(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            floors = build_floor_definitions("building-a", Path(temp_dir) / "assets")
        graph = BuildingGraph(floors)
        agent = initial_agents(graph, [("track-1", "track-1")], 42)[0]
        payload = agent_location_payload(
            agent,
            graph,
            {floor.floor_number: floor for floor in floors},
            "provider-a",
            datetime.now(UTC),
            0.0,
        )
        self.assertEqual(payload["crs"], "EPSG:4326")
        self.assertEqual(len(payload["position"]["coordinates"]), 3)
        self.assertIn("local_position", payload["properties"])
        self.assertEqual(payload["source"], floors[0].zone_id)


if __name__ == "__main__":
    unittest.main()
