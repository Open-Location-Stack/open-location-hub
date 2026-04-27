"""Helpers for talking to a local Open RTLS Hub from connector scripts."""

from __future__ import annotations

import json
import logging
import threading
import uuid
from dataclasses import dataclass
from typing import Any
from urllib.parse import urljoin

import requests
import websocket


LOGGER = logging.getLogger(__name__)
NAMESPACE = uuid.UUID("4c7dbff3-5292-4e9f-bf4b-cff6e322e6af")


def deterministic_uuid(kind: str, external_id: str) -> str:
    return str(uuid.uuid5(NAMESPACE, f"{kind}:{external_id}"))


def point(x: float, y: float, z: float | None = None) -> dict[str, Any]:
    coordinates: list[float] = [x, y]
    if z is not None:
        coordinates.append(z)
    return {"type": "Point", "coordinates": coordinates}


@dataclass
class HubConfig:
    ws_url: str
    http_url: str | None = None
    token: str | None = None
    timeout_seconds: float = 30.0


class HubRESTClient:
    """Best-effort idempotent CRUD helpers for connector-managed resources."""

    def __init__(self, config: HubConfig):
        self.config = config
        self.session = requests.Session()
        self.session.headers.update({"Accept": "application/json"})
        if config.token:
            self.session.headers.update({"Authorization": f"Bearer {config.token}"})

    def ensure_provider(
        self,
        provider_id: str,
        provider_type: str,
        name: str,
        properties: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        if not self.config.http_url:
            return {}
        payload = {
            "id": provider_id,
            "type": provider_type,
            "name": name,
            "properties": properties or {},
        }
        return self._ensure_resource("/v2/providers", f"/v2/providers/{provider_id}", payload)

    def ensure_trackable(
        self,
        trackable_id: str,
        name: str,
        provider_id: str,
        radius: float | None = None,
        properties: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        if not self.config.http_url:
            return {}
        payload = {
            "id": trackable_id,
            "type": "virtual",
            "name": name,
            "location_providers": [provider_id],
            "properties": properties or {},
        }
        if radius is not None:
            payload["radius"] = radius
        return self._ensure_resource("/v2/trackables", f"/v2/trackables/{trackable_id}", payload)

    def ensure_zone(self, zone_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        if not self.config.http_url:
            return {}
        return self._ensure_resource("/v2/zones", f"/v2/zones/{zone_id}", payload)

    def ensure_fence(self, fence_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        if not self.config.http_url:
            return {}
        return self._ensure_resource("/v2/fences", f"/v2/fences/{fence_id}", payload)

    def _ensure_resource(self, collection_path: str, item_path: str, payload: dict[str, Any]) -> dict[str, Any]:
        existing = self._request("GET", item_path, expected={200, 404})
        if existing.status_code == 404:
            return self._request("POST", collection_path, json_body=payload, expected={201}).json()
        return self._request("PUT", item_path, json_body=payload, expected={200}).json()

    def _request(
        self,
        method: str,
        path: str,
        json_body: dict[str, Any] | None = None,
        expected: set[int] | None = None,
    ) -> requests.Response:
        if not self.config.http_url:
            raise RuntimeError("Hub REST operations require HUB_HTTP_URL")
        response = self.session.request(
            method=method,
            url=urljoin(self.config.http_url.rstrip("/") + "/", path.lstrip("/")),
            json=json_body,
            timeout=self.config.timeout_seconds,
        )
        if expected and response.status_code not in expected:
            try:
                details = response.json()
            except ValueError:
                details = response.text
            raise RuntimeError(f"{method} {path} returned {response.status_code}: {details}")
        return response


class HubWebSocketPublisher:
    """Send OMLOX wrapper messages to the hub over WebSocket."""

    def __init__(self, config: HubConfig):
        self.config = config
        self._connection: websocket.WebSocket | None = None
        self._lock = threading.Lock()
        self._keepalive_stop = threading.Event()
        self._keepalive_thread = threading.Thread(target=self._keepalive_loop, name="hub-ws-keepalive", daemon=True)
        self._keepalive_thread.start()

    def close(self) -> None:
        self._keepalive_stop.set()
        with self._lock:
            self._close_locked()
        self._keepalive_thread.join(timeout=1.0)

    def publish_locations(self, locations: list[dict[str, Any]]) -> None:
        if not locations:
            return
        message: dict[str, Any] = {
            "event": "message",
            "topic": "location_updates",
            "payload": locations,
        }
        if self.config.token:
            message["params"] = {"token": self.config.token}
        self._send(json.dumps(message))

    def _connect(self) -> websocket.WebSocket:
        LOGGER.info("connecting websocket publisher to %s", self.config.ws_url)
        connection = websocket.create_connection(self.config.ws_url, timeout=self.config.timeout_seconds)
        connection.settimeout(self.config.timeout_seconds)
        return connection

    def _send(self, raw: str) -> None:
        with self._lock:
            if self._connection is None:
                self._connection = self._connect()
            try:
                self._connection.send(raw)
            except Exception:
                LOGGER.info("websocket send failed; reconnecting")
                self._close_locked()
                self._connection = self._connect()
                self._connection.send(raw)

    def _keepalive_loop(self) -> None:
        while not self._keepalive_stop.wait(15.0):
            with self._lock:
                if self._connection is None:
                    continue
                try:
                    self._connection.ping("keepalive")
                except Exception:
                    LOGGER.info("websocket ping failed; reconnecting on next send")
                    self._close_locked()

    def _close_locked(self) -> None:
        if self._connection is not None:
            try:
                self._connection.close()
            except Exception:
                LOGGER.debug("websocket close failed", exc_info=True)
            self._connection = None
