"""Helpers for replaying logged hub location NDJSON streams."""

from __future__ import annotations

import json
import math
import os
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any


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


@dataclass(frozen=True)
class LoggedLocation:
    order: int
    timestamp: datetime
    location: dict[str, Any]


@dataclass(frozen=True)
class ReplayLocation:
    order: float
    original_timestamp: datetime
    replay_timestamp: datetime
    location: dict[str, Any]
    synthetic: bool


def load_logged_locations(path: str) -> list[LoggedLocation]:
    logged_locations: list[LoggedLocation] = []
    input_path = Path(path)
    with input_path.open("r", encoding="utf-8") as handle:
        for line_number, raw_line in enumerate(handle, start=1):
            stripped = raw_line.strip()
            if not stripped:
                continue
            wrapper = json.loads(stripped)
            message = wrapper.get("message")
            if not isinstance(message, dict):
                continue
            payload = message.get("payload")
            if not isinstance(payload, list):
                continue
            fallback_timestamp = parse_timestamp(wrapper.get("received_at"))
            for payload_index, raw_location in enumerate(payload):
                if not isinstance(raw_location, dict):
                    continue
                timestamp = parse_timestamp(raw_location.get("timestamp_generated")) or fallback_timestamp
                if timestamp is None:
                    raise ValueError(f"{path}:{line_number} is missing both timestamp_generated and received_at")
                logged_locations.append(
                    LoggedLocation(
                        order=(line_number * 1000) + payload_index,
                        timestamp=timestamp,
                        location=raw_location,
                    )
                )
    if not logged_locations:
        raise ValueError(f"{path} did not contain any replayable location payloads")
    return sorted(logged_locations, key=lambda item: (item.timestamp, item.order))


def build_replay_schedule(
    logged_locations: list[LoggedLocation],
    replay_start: datetime,
    acceleration_factor: float,
    interpolation_rate_hz: float,
) -> list[ReplayLocation]:
    if acceleration_factor <= 0:
        raise ValueError("acceleration_factor must be greater than 0")
    if interpolation_rate_hz < 0:
        raise ValueError("interpolation_rate_hz must be greater than or equal to 0")

    expanded = interpolate_logged_locations(logged_locations, interpolation_rate_hz)
    baseline = expanded[0].timestamp

    replay_schedule: list[ReplayLocation] = []
    for item in expanded:
        replay_offset = (item.timestamp - baseline).total_seconds() / acceleration_factor
        replay_timestamp = replay_start + timedelta(seconds=replay_offset)
        replay_schedule.append(
            ReplayLocation(
                order=float(item.order),
                original_timestamp=item.timestamp,
                replay_timestamp=replay_timestamp,
                location=prepare_location_for_replay(item.location, item.timestamp, replay_timestamp, item.synthetic),
                synthetic=item.synthetic,
            )
        )
    return replay_schedule


@dataclass(frozen=True)
class ReplaySchedulePreview:
    scheduled_locations: int
    synthetic_locations: int
    source_span_seconds: float


@dataclass(frozen=True)
class ExpandedLocation:
    order: float
    timestamp: datetime
    location: dict[str, Any]
    synthetic: bool


def interpolate_logged_locations(
    logged_locations: list[LoggedLocation],
    interpolation_rate_hz: float,
) -> list[ExpandedLocation]:
    if interpolation_rate_hz <= 0:
        return [
            ExpandedLocation(
                order=float(item.order),
                timestamp=item.timestamp,
                location=item.location,
                synthetic=False,
            )
            for item in logged_locations
        ]

    interval_seconds = 1.0 / interpolation_rate_hz
    expanded: list[ExpandedLocation] = []
    previous_by_key: dict[str, LoggedLocation] = {}

    for item in logged_locations:
        key = replay_object_key(item.location)
        previous = previous_by_key.get(key)
        if previous is not None:
            elapsed = (item.timestamp - previous.timestamp).total_seconds()
            if elapsed > interval_seconds:
                step_count = int(elapsed / interval_seconds)
                for step in range(1, step_count + 1):
                    interpolated_offset = step * interval_seconds
                    if interpolated_offset >= elapsed:
                        break
                    fraction = interpolated_offset / elapsed
                    interpolated_timestamp = previous.timestamp + timedelta(seconds=interpolated_offset)
                    expanded.append(
                        ExpandedLocation(
                            order=previous.order + fraction,
                            timestamp=interpolated_timestamp,
                            location=interpolate_location(previous.location, item.location, fraction),
                            synthetic=True,
                        )
                    )
        expanded.append(
            ExpandedLocation(
                order=float(item.order),
                timestamp=item.timestamp,
                location=item.location,
                synthetic=False,
            )
        )
        previous_by_key[key] = item

    return expanded


def preview_replay_schedule(
    logged_locations: list[LoggedLocation],
    interpolation_rate_hz: float,
) -> ReplaySchedulePreview:
    if not logged_locations:
        raise ValueError("logged_locations must not be empty")
    if interpolation_rate_hz < 0:
        raise ValueError("interpolation_rate_hz must be greater than or equal to 0")

    synthetic_locations = 0
    scheduled_locations = len(logged_locations)
    if interpolation_rate_hz > 0:
        interval_seconds = 1.0 / interpolation_rate_hz
        previous_by_key: dict[str, LoggedLocation] = {}
        for item in logged_locations:
            key = replay_object_key(item.location)
            previous = previous_by_key.get(key)
            if previous is not None:
                elapsed = (item.timestamp - previous.timestamp).total_seconds()
                if elapsed > interval_seconds:
                    synthetic_locations += int(math.nextafter(elapsed, 0.0) / interval_seconds)
            previous_by_key[key] = item
        scheduled_locations += synthetic_locations

    source_span_seconds = (logged_locations[-1].timestamp - logged_locations[0].timestamp).total_seconds()
    return ReplaySchedulePreview(
        scheduled_locations=scheduled_locations,
        synthetic_locations=synthetic_locations,
        source_span_seconds=source_span_seconds,
    )


def interpolate_location(previous: dict[str, Any], current: dict[str, Any], fraction: float) -> dict[str, Any]:
    previous_coordinates = coordinates(previous)
    current_coordinates = coordinates(current)

    synthetic = dict(current)
    synthetic["position"] = {
        "type": "Point",
        "coordinates": [
            interpolate_float(previous_coordinates[0], current_coordinates[0], fraction),
            interpolate_float(previous_coordinates[1], current_coordinates[1], fraction),
        ],
    }
    if previous.get("speed") is not None and current.get("speed") is not None:
        synthetic["speed"] = interpolate_float(float(previous["speed"]), float(current["speed"]), fraction)
    if previous.get("course") is not None and current.get("course") is not None:
        synthetic["course"] = interpolate_float(float(previous["course"]), float(current["course"]), fraction)
    return synthetic


def prepare_location_for_replay(
    location: dict[str, Any],
    original_timestamp: datetime,
    replay_timestamp: datetime,
    synthetic: bool,
) -> dict[str, Any]:
    replay_location = dict(location)
    replay_location["timestamp_generated"] = replay_timestamp.astimezone(UTC).isoformat()
    raw_properties = replay_location.get("properties")
    properties = dict(raw_properties) if isinstance(raw_properties, dict) else {}
    properties["replay_original_timestamp_generated"] = original_timestamp.astimezone(UTC).isoformat()
    properties["replay_synthetic_interpolation"] = synthetic
    replay_location["properties"] = properties
    return replay_location


def replay_object_key(location: dict[str, Any]) -> str:
    trackables = location.get("trackables")
    if isinstance(trackables, list) and trackables:
        return f"trackable:{trackables[0]}"
    source = location.get("source")
    if isinstance(source, str) and source:
        return f"source:{source}"
    provider_id = location.get("provider_id") or "provider"
    coordinates_value = coordinates(location)
    return f"fallback:{provider_id}:{coordinates_value[0]}:{coordinates_value[1]}"


def coordinates(location: dict[str, Any]) -> tuple[float, float]:
    position = location.get("position")
    if not isinstance(position, dict) or position.get("type") != "Point":
        raise ValueError("replay connector only supports Point locations")
    coordinates_value = position.get("coordinates")
    if (
        not isinstance(coordinates_value, list)
        or len(coordinates_value) < 2
        or coordinates_value[0] is None
        or coordinates_value[1] is None
    ):
        raise ValueError("location is missing valid Point coordinates")
    return float(coordinates_value[0]), float(coordinates_value[1])


def parse_timestamp(raw_value: Any) -> datetime | None:
    if not isinstance(raw_value, str) or not raw_value:
        return None
    normalized = raw_value.strip()
    if normalized.endswith("Z"):
        normalized = normalized[:-1] + "+00:00"
    parsed = datetime.fromisoformat(normalized)
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=UTC)
    return parsed.astimezone(UTC)


def interpolate_float(previous: float, current: float, fraction: float) -> float:
    return previous + ((current - previous) * fraction)
