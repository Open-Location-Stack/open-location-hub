"""Corridor-graph movement simulator for the mock UWB building demo."""

from __future__ import annotations

import math
import random
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from uwb_support import FloorDefinition, floor_zone_properties


TRACKABLE_RADIUS_METERS = 0.5


@dataclass(frozen=True)
class GraphNode:
    id: str
    label: str
    floor_number: int
    x: float
    y: float
    z: float


@dataclass(frozen=True)
class GraphEdge:
    start: str
    end: str
    length: float


@dataclass
class AgentState:
    trackable_id: str
    name: str
    from_node: str
    to_node: str
    distance_on_edge: float
    lateral_amplitude: float
    lateral_frequency_hz: float
    lateral_phase_radians: float
    vertical_amplitude: float
    vertical_frequency_hz: float
    vertical_phase_radians: float
    base_speed_mps: float
    speed_variation_mps: float
    speed_frequency_hz: float
    speed_phase_radians: float
    random_source: random.Random


class BuildingGraph:
    def __init__(self, floors: list[FloorDefinition]):
        self.floors = floors
        self.floor_by_number = {floor.floor_number: floor for floor in floors}
        self.nodes: dict[str, GraphNode] = {}
        self.adjacency: dict[str, list[str]] = {}
        self.edge_lengths: dict[tuple[str, str], float] = {}
        self._build(floors)

    def _build(self, floors: list[FloorDefinition]) -> None:
        for floor in floors:
            for label, (x, y, z) in floor.nodes.items():
                node_id = self.node_id(floor.floor_number, label)
                self.nodes[node_id] = GraphNode(
                    id=node_id,
                    label=label,
                    floor_number=floor.floor_number,
                    x=x,
                    y=y,
                    z=z,
                )
                self.adjacency.setdefault(node_id, [])
            for start_label, end_label in floor.edges:
                self.add_edge(self.node_id(floor.floor_number, start_label), self.node_id(floor.floor_number, end_label))

        for floor in floors[:-1]:
            current = self.node_id(floor.floor_number, "connector")
            upper = self.node_id(floor.floor_number + 1, "connector")
            self.add_edge(current, upper)

    @staticmethod
    def node_id(floor_number: int, label: str) -> str:
        return f"f{floor_number}:{label}"

    def add_edge(self, start: str, end: str) -> None:
        start_node = self.nodes[start]
        end_node = self.nodes[end]
        length = math.dist((start_node.x, start_node.y, start_node.z), (end_node.x, end_node.y, end_node.z))
        self.edge_lengths[(start, end)] = length
        self.edge_lengths[(end, start)] = length
        self.adjacency[start].append(end)
        self.adjacency[end].append(start)

    def edge_length(self, start: str, end: str) -> float:
        return self.edge_lengths[(start, end)]


def initial_agents(graph: BuildingGraph, trackables: list[tuple[str, str]], seed: int) -> list[AgentState]:
    anchor_edges = [
        (1, "center", "connector"),
        (1, "upper_arc_1", "upper_arc_2"),
        (2, "center", "top_dead"),
        (2, "left_arc_2", "left_arc_3"),
        (3, "mouth_lower", "lower_arc_2"),
    ]
    agents: list[AgentState] = []
    for index, (trackable_id, name) in enumerate(trackables):
        pair_index = (index // 2) % len(anchor_edges)
        floor_number, start_label, end_label = anchor_edges[pair_index]
        from_node = graph.node_id(floor_number, start_label)
        to_node = graph.node_id(floor_number, end_label)
        rng = random.Random(seed + index)
        base_progress = 0.25 + (0.08 * (index % 2))
        edge_length = graph.edge_length(from_node, to_node)
        agents.append(
            AgentState(
                trackable_id=trackable_id,
                name=name,
                from_node=from_node,
                to_node=to_node,
                distance_on_edge=edge_length * base_progress,
                lateral_amplitude=0.18 + (0.05 * rng.random()),
                lateral_frequency_hz=0.18 + (0.18 * rng.random()),
                lateral_phase_radians=2 * math.pi * rng.random(),
                vertical_amplitude=0.10 + (0.20 * rng.random()),
                vertical_frequency_hz=0.09 + (0.16 * rng.random()),
                vertical_phase_radians=2 * math.pi * rng.random(),
                base_speed_mps=1.0 + (0.8 * rng.random()),
                speed_variation_mps=0.20 + (0.35 * rng.random()),
                speed_frequency_hz=0.03 + (0.12 * rng.random()),
                speed_phase_radians=2 * math.pi * rng.random(),
                random_source=rng,
            )
        )
    return agents


def choose_next_node(graph: BuildingGraph, current: str, previous: str, rng: random.Random) -> str:
    neighbors = graph.adjacency[current]
    forward_options = [neighbor for neighbor in neighbors if neighbor != previous]
    if not forward_options:
        return previous

    current_node = graph.nodes[current]
    if current_node.label == "connector":
        vertical = [neighbor for neighbor in forward_options if graph.nodes[neighbor].floor_number != current_node.floor_number]
        same_floor = [neighbor for neighbor in forward_options if graph.nodes[neighbor].floor_number == current_node.floor_number]
        if vertical and same_floor:
            if rng.random() < 0.5:
                return rng.choice(vertical)
            return rng.choice(same_floor)
    return rng.choice(forward_options)


def current_speed(agent: AgentState, elapsed_seconds: float) -> float:
    value = agent.base_speed_mps + agent.speed_variation_mps * math.sin((2 * math.pi * agent.speed_frequency_hz * elapsed_seconds) + agent.speed_phase_radians)
    return max(0.4, value)


def advance_agent(agent: AgentState, graph: BuildingGraph, dt_seconds: float, elapsed_seconds: float) -> None:
    remaining_distance = current_speed(agent, elapsed_seconds) * dt_seconds
    while remaining_distance > 1e-9:
        edge_length = graph.edge_length(agent.from_node, agent.to_node)
        remaining_on_edge = edge_length - agent.distance_on_edge
        if remaining_distance < remaining_on_edge:
            agent.distance_on_edge += remaining_distance
            return

        remaining_distance -= remaining_on_edge
        current = agent.to_node
        previous = agent.from_node
        agent.from_node = current
        agent.to_node = choose_next_node(graph, current, previous, agent.random_source)
        agent.distance_on_edge = 0.0


def agent_location_payload(
    agent: AgentState,
    graph: BuildingGraph,
    floors_by_number: dict[int, FloorDefinition],
    provider_id: str,
    at: datetime,
    elapsed_seconds: float,
) -> dict[str, Any]:
    from_node = graph.nodes[agent.from_node]
    to_node = graph.nodes[agent.to_node]
    edge_length = graph.edge_length(agent.from_node, agent.to_node)
    alpha = 0.0 if edge_length <= 0 else min(max(agent.distance_on_edge / edge_length, 0.0), 1.0)

    x = from_node.x + ((to_node.x - from_node.x) * alpha)
    y = from_node.y + ((to_node.y - from_node.y) * alpha)
    z = from_node.z + ((to_node.z - from_node.z) * alpha)

    dx = to_node.x - from_node.x
    dy = to_node.y - from_node.y
    horizontal_norm = math.hypot(dx, dy)
    if horizontal_norm > 1e-9:
        perpendicular_x = -dy / horizontal_norm
        perpendicular_y = dx / horizontal_norm
        lateral_offset = agent.lateral_amplitude * math.sin((2 * math.pi * agent.lateral_frequency_hz * elapsed_seconds) + agent.lateral_phase_radians)
        x += perpendicular_x * lateral_offset
        y += perpendicular_y * lateral_offset

    z += agent.vertical_amplitude * math.sin((2 * math.pi * agent.vertical_frequency_hz * elapsed_seconds) + agent.vertical_phase_radians)

    floor_number = resolved_floor_number(from_node.floor_number, to_node.floor_number, alpha)
    floor = floors_by_number[floor_number]
    zone_properties = floor_zone_properties(floor)

    payload: dict[str, Any] = {
        "crs": "local",
        "position": {"type": "Point", "coordinates": [round(x, 4), round(y, 4), round(z, 4)]},
        "provider_id": provider_id,
        "provider_type": "uwb",
        "source": floor.zone_id,
        "timestamp_generated": at.isoformat().replace("+00:00", "Z"),
        "trackables": [agent.trackable_id],
        "floor": float(floor_number),
        "properties": {
            "connector": "uwb_sim",
            "object_name": agent.name,
            "object_speed_mps": round(current_speed(agent, elapsed_seconds), 4),
            "graph_edge": f"{from_node.label}->{to_node.label}",
            "active_floor": floor_number,
            "zone_id": floor.zone_id,
            "floorplan_id": floor.floorplan_id,
            "floorplan_image_path": zone_properties["floorplan_image_path"],
        },
    }
    return payload


def resolved_floor_number(from_floor: int, to_floor: int, alpha: float) -> int:
    if from_floor == to_floor:
        return from_floor
    return from_floor if alpha < 0.5 else to_floor
