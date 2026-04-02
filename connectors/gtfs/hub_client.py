"""Helpers for talking to a local Open RTLS Hub from connector scripts."""

from __future__ import annotations

import json
import logging
import threading
import time
import uuid
from dataclasses import dataclass
from typing import Any
from urllib.parse import urljoin, urlparse

import paho.mqtt.client as mqtt
import requests
import websocket


LOGGER = logging.getLogger(__name__)
NAMESPACE = uuid.UUID("bfc6b8ac-84f4-49e1-a2b4-26f8a9573fd4")


def deterministic_uuid(kind: str, external_id: str) -> str:
    """Return a stable UUIDv5 for a connector-managed resource."""

    return str(uuid.uuid5(NAMESPACE, f"{kind}:{external_id}"))


def point(longitude: float, latitude: float) -> dict[str, Any]:
    """Return a GeoJSON point."""

    return {"type": "Point", "coordinates": [longitude, latitude]}


@dataclass
class HubConfig:
    http_url: str
    ws_url: str | None = None
    mqtt_broker_url: str | None = None
    mqtt_client_id: str | None = None
    mqtt_username: str | None = None
    mqtt_password: str | None = None
    mqtt_keepalive_seconds: int = 30
    mqtt_qos: int = 1
    token: str | None = None
    timeout_seconds: float = 30.0


class HubRESTClient:
    """HubRESTClient wraps idempotent CRUD helpers for connector resources."""

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
        payload = {
            "id": provider_id,
            "type": provider_type,
            "name": name,
            "properties": properties or {},
        }
        return self._ensure_resource(
            collection_path="/v2/providers",
            item_path=f"/v2/providers/{provider_id}",
            payload=payload,
        )

    def ensure_trackable(
        self,
        trackable_id: str,
        name: str,
        provider_id: str,
        properties: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        payload = {
            "id": trackable_id,
            "type": "virtual",
            "name": name,
            "location_providers": [provider_id],
            "properties": properties or {},
        }
        return self._ensure_resource(
            collection_path="/v2/trackables",
            item_path=f"/v2/trackables/{trackable_id}",
            payload=payload,
        )

    def ensure_zone(self, zone_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return self._ensure_resource(
            collection_path="/v2/zones",
            item_path=f"/v2/zones/{zone_id}",
            payload=payload,
        )

    def ensure_fence(self, fence_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return self._ensure_resource(
            collection_path="/v2/fences",
            item_path=f"/v2/fences/{fence_id}",
            payload=payload,
        )

    def _ensure_resource(
        self,
        collection_path: str,
        item_path: str,
        payload: dict[str, Any],
    ) -> dict[str, Any]:
        existing = self._request("GET", item_path, expected={200, 404})
        if existing.status_code == 404:
            response = self._request("POST", collection_path, json_body=payload, expected={201})
            return response.json()

        response = self._request("PUT", item_path, json_body=payload, expected={200})
        return response.json()

    def _request(
        self,
        method: str,
        path: str,
        json_body: dict[str, Any] | None = None,
        expected: set[int] | None = None,
    ) -> requests.Response:
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
            raise RuntimeError(
                f"{method} {path} returned {response.status_code}: {details}"
            )
        return response


class HubWebSocketPublisher:
    """HubWebSocketPublisher sends OMLOX wrapper messages to location_updates."""

    def __init__(self, config: HubConfig):
        self.config = config
        self._connection: websocket.WebSocket | None = None
        self._lock = threading.Lock()
        self._keepalive_stop = threading.Event()
        self._keepalive_thread = threading.Thread(
            target=self._keepalive_loop,
            name="hub-ws-keepalive",
            daemon=True,
        )
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

        raw = json.dumps(message)
        self._send(raw)

    def _connect(self) -> websocket.WebSocket:
        LOGGER.info("connecting websocket publisher to %s", self.config.ws_url)
        connection = websocket.create_connection(
            self.config.ws_url,
            timeout=self.config.timeout_seconds,
        )
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


class HubMQTTPublisher:
    """HubMQTTPublisher publishes OMLOX location payloads over MQTT."""

    def __init__(self, config: HubConfig):
        if not config.mqtt_broker_url:
            raise ValueError("mqtt_broker_url is required for MQTT publishing")

        self.config = config
        self._client = mqtt.Client(
            callback_api_version=mqtt.CallbackAPIVersion.VERSION2,
            client_id=config.mqtt_client_id or "",
        )
        if config.mqtt_username:
            self._client.username_pw_set(config.mqtt_username, config.mqtt_password)
        self._client.reconnect_delay_set(min_delay=1, max_delay=30)
        self._client.on_connect = self._on_connect
        self._client.on_disconnect = self._on_disconnect

        self._connected = threading.Event()
        self._stopped = False
        self._connect()
        self._client.loop_start()

    def close(self) -> None:
        self._stopped = True
        self._client.loop_stop()
        try:
            self._client.disconnect()
        except Exception:
            LOGGER.debug("mqtt disconnect failed", exc_info=True)

    def publish_locations(self, provider_id: str, locations: list[dict[str, Any]]) -> None:
        if not locations:
            return
        self._wait_connected()
        topic = f"/omlox/json/location_updates/pub/{provider_id}"
        for location in locations:
            payload = json.dumps(location)
            info = self._client.publish(topic, payload=payload, qos=self.config.mqtt_qos, retain=False)
            info.wait_for_publish()
            if info.rc != mqtt.MQTT_ERR_SUCCESS:
                raise RuntimeError(f"mqtt publish failed for {topic}: rc={info.rc}")

    def _connect(self) -> None:
        parsed = urlparse(self.config.mqtt_broker_url or "")
        if parsed.scheme not in {"tcp", "mqtt"}:
            raise ValueError(
                f"unsupported MQTT broker URL scheme {parsed.scheme!r}; use tcp:// or mqtt://"
            )
        host = parsed.hostname
        if not host:
            raise ValueError("MQTT broker URL must include a hostname")
        port = parsed.port or 1883
        LOGGER.info("connecting mqtt publisher to %s", self.config.mqtt_broker_url)
        self._client.connect(host, port=port, keepalive=self.config.mqtt_keepalive_seconds)

    def _wait_connected(self) -> None:
        if not self._connected.wait(timeout=self.config.timeout_seconds):
            raise RuntimeError("mqtt connect timed out")

    def _on_connect(
        self,
        _client: mqtt.Client,
        _userdata: Any,
        _flags: mqtt.ConnectFlags,
        reason_code: mqtt.ReasonCode,
        _properties: mqtt.Properties | None = None,
    ) -> None:
        if reason_code.is_failure:
            LOGGER.warning("mqtt connect failed: %s", reason_code)
            self._connected.clear()
            return
        LOGGER.info("mqtt connected")
        self._connected.set()

    def _on_disconnect(
        self,
        _client: mqtt.Client,
        _userdata: Any,
        _flags: mqtt.DisconnectFlags,
        reason_code: mqtt.ReasonCode,
        _properties: mqtt.Properties | None = None,
    ) -> None:
        self._connected.clear()
        if not self._stopped:
            LOGGER.warning("mqtt disconnected: %s", reason_code)
