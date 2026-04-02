#!/usr/bin/env python3

import argparse
import json
import sys
import urllib.parse
import urllib.request
import uuid
from copy import deepcopy


DEFAULT_LOG_FIELDS = [
    {
        "dataType": "",
        "fieldContext": "log",
        "fieldDataType": "",
        "isIndexed": False,
        "name": "timestamp",
        "signal": "logs",
        "type": "log",
    },
    {
        "dataType": "",
        "fieldContext": "log",
        "fieldDataType": "",
        "isIndexed": False,
        "name": "body",
        "signal": "logs",
        "type": "log",
    },
]

DEFAULT_TRACE_FIELDS = [
    {
        "fieldContext": "resource",
        "fieldDataType": "string",
        "name": "service.name",
        "signal": "traces",
    },
    {
        "fieldContext": "span",
        "fieldDataType": "string",
        "name": "name",
        "signal": "traces",
    },
    {
        "fieldContext": "span",
        "fieldDataType": "",
        "name": "duration_nano",
        "signal": "traces",
    },
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Bootstrap local SigNoz dashboards.")
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--email", required=True)
    parser.add_argument("--password", required=True)
    return parser.parse_args()


def http_json(url: str, *, method: str = "GET", headers=None, body=None):
    request = urllib.request.Request(url, method=method, headers=headers or {})
    if body is not None:
        request.data = json.dumps(body).encode()
    with urllib.request.urlopen(request) as response:
        if response.status == 204:
            return None
        return json.load(response)


def signoz_token(base_url: str, email: str, password: str) -> str:
    context = http_json(
        f"{base_url}/api/v2/sessions/context?{urllib.parse.urlencode({'email': email, 'ref': base_url})}"
    )
    orgs = ((context.get("data") or {}).get("orgs") or [{}])
    org_id = orgs[0].get("id", "")
    if not org_id:
        raise RuntimeError(f"could not resolve SigNoz org for {email}")
    login = http_json(
        f"{base_url}/api/v2/sessions/email_password",
        method="POST",
        headers={"Content-Type": "application/json"},
        body={"email": email, "password": password, "orgId": org_id},
    )
    token = ((login.get("data") or {}).get("accessToken")) or ""
    if not token:
        raise RuntimeError(f"could not authenticate SigNoz user {email}")
    return token


def ui_groupby(name: str, data_type: str = "string") -> dict:
    return {
        "key": name,
        "type": "tag",
        "dataType": data_type,
        "isColumn": False,
        "isJSON": False,
        "id": f"{name}--{data_type}--tag--false",
    }


def metric_query(
    metric_name: str,
    expression: str,
    *,
    space_aggregation: str = "sum",
    time_aggregation: str = "avg",
    group_by=None,
    filter_expression: str = "",
) -> dict:
    return {
        "aggregations": [
            {
                "metricName": metric_name,
                "reduceTo": space_aggregation,
                "spaceAggregation": space_aggregation,
                "temporality": "",
                "timeAggregation": time_aggregation,
            }
        ],
        "dataSource": "metrics",
        "disabled": False,
        "expression": expression,
        "filter": {"expression": filter_expression},
        "functions": [],
        "groupBy": [ui_groupby(name) for name in (group_by or [])],
        "having": {"expression": ""},
        "legend": "",
        "limit": None,
        "orderBy": [],
        "queryName": expression,
        "source": "",
        "stepInterval": None,
    }


def widget(title: str, queries: list[dict], *, y_axis_unit: str = "") -> dict:
    query_id = str(uuid.uuid4())
    query_names = [query["expression"] for query in queries]
    return {
        "bucketCount": 30,
        "bucketWidth": 0,
        "columnUnits": {},
        "contextLinks": {"linksData": []},
        "customLegendColors": {},
        "decimalPrecision": 2,
        "description": "",
        "fillMode": "none",
        "fillSpans": False,
        "id": str(uuid.uuid4()),
        "isLogScale": False,
        "legendPosition": "bottom",
        "lineInterpolation": "spline",
        "lineStyle": "solid",
        "mergeAllActiveQueries": False,
        "nullZeroValues": "zero",
        "opacity": "1",
        "panelTypes": "graph",
        "query": {
            "builder": {
                "queryData": queries,
                "queryFormulas": [],
                "queryTraceOperator": [],
            },
            "clickhouse_sql": [
                {"disabled": False, "legend": "", "name": name, "query": ""}
                for name in query_names
            ],
            "id": query_id,
            "promql": [
                {"disabled": False, "legend": "", "name": name, "query": ""}
                for name in query_names
            ],
            "queryType": "builder",
            "unit": "",
        },
        "selectedLogFields": deepcopy(DEFAULT_LOG_FIELDS),
        "selectedTracesFields": deepcopy(DEFAULT_TRACE_FIELDS),
        "showPoints": False,
        "softMax": 0,
        "softMin": 0,
        "spanGaps": True,
        "stackedBarChart": False,
        "thresholds": [],
        "timePreferance": "GLOBAL_TIME",
        "title": title,
        "yAxisUnit": y_axis_unit,
    }


def dashboard(title: str, widgets: list[dict]) -> dict:
    layout = []
    for index, item in enumerate(widgets):
        layout.append(
            {
                "h": 6,
                "i": item["id"],
                "moved": False,
                "static": False,
                "w": 6,
                "x": (index % 2) * 6,
                "y": (index // 2) * 6,
            }
        )
    return {
        "title": title,
        "version": "v5",
        "uploadedGrafana": False,
        "panelMap": {},
        "layout": layout,
        "widgets": widgets,
    }


def desired_dashboards() -> list[dict]:
    return [
        dashboard(
            "Open RTLS Hub Throughput",
            [
                widget(
                    "Ingest Rate by Outcome",
                    [
                        metric_query(
                            "hub.ingest.records_total",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["outcome"],
                        )
                    ],
                ),
                widget(
                    "Ingest Rate by Transport",
                    [
                        metric_query(
                            "hub.ingest.records_total",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["transport"],
                        )
                    ],
                ),
                widget(
                    "Dependency Event Rate by Dependency",
                    [
                        metric_query(
                            "hub.runtime.dependency_events_total",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["dependency"],
                        )
                    ],
                ),
                widget(
                    "RPC Requests Rate by Method",
                    [
                        metric_query(
                            "hub.rpc.requests_total",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["method"],
                        )
                    ],
                ),
                widget(
                    "Queue Depths",
                    [
                        metric_query(
                            "hub.runtime.native_queue_depth",
                            "A",
                            space_aggregation="avg",
                            time_aggregation="latest",
                        ),
                        metric_query(
                            "hub.runtime.decision_queue_depth",
                            "B",
                            space_aggregation="avg",
                            time_aggregation="latest",
                        ),
                        metric_query(
                            "hub.runtime.websocket_outbound_depth",
                            "C",
                            space_aggregation="avg",
                            time_aggregation="latest",
                        ),
                    ],
                ),
                widget(
                    "Connections and Subscribers",
                    [
                        metric_query(
                            "hub.runtime.websocket_connections",
                            "A",
                            space_aggregation="avg",
                            time_aggregation="latest",
                        ),
                        metric_query(
                            "hub.runtime.event_bus_subscribers",
                            "B",
                            space_aggregation="avg",
                            time_aggregation="latest",
                        ),
                    ],
                ),
            ],
        ),
        dashboard(
            "Open RTLS Hub Latency",
            [
                widget(
                    "End-to-End Latency p99 by Event Kind",
                    [
                        metric_query(
                            "hub.processing.end_to_end_duration.bucket",
                            "A",
                            space_aggregation="p99",
                            time_aggregation="avg",
                            group_by=["event_kind"],
                        )
                    ],
                    y_axis_unit="s",
                ),
                widget(
                    "Processing Latency p99 by Stage",
                    [
                        metric_query(
                            "hub.processing.duration.bucket",
                            "A",
                            space_aggregation="p99",
                            time_aggregation="avg",
                            group_by=["stage"],
                        )
                    ],
                    y_axis_unit="s",
                ),
                widget(
                    "Queue Wait p99 by Stage",
                    [
                        metric_query(
                            "hub.processing.queue_wait_duration.bucket",
                            "A",
                            space_aggregation="p99",
                            time_aggregation="avg",
                            group_by=["stage"],
                        )
                    ],
                    y_axis_unit="s",
                ),
                widget(
                    "Event Bus Emit p99 by Event Kind",
                    [
                        metric_query(
                            "hub.event_bus.emit_duration.bucket",
                            "A",
                            space_aggregation="p99",
                            time_aggregation="avg",
                            group_by=["event_kind"],
                        )
                    ],
                    y_axis_unit="s",
                ),
                widget(
                    "MQTT Publish p99 by Outcome",
                    [
                        metric_query(
                            "hub.mqtt.publish_duration.bucket",
                            "A",
                            space_aggregation="p99",
                            time_aggregation="avg",
                            group_by=["outcome"],
                        )
                    ],
                    y_axis_unit="s",
                ),
                widget(
                    "RPC Duration p99 by Method",
                    [
                        metric_query(
                            "hub.rpc.duration.bucket",
                            "A",
                            space_aggregation="p99",
                            time_aggregation="avg",
                            group_by=["method"],
                        )
                    ],
                    y_axis_unit="s",
                ),
            ],
        ),
        dashboard(
            "Open RTLS Hub Outcomes",
            [
                widget(
                    "Ingest Accepted vs Deduplicated",
                    [
                        metric_query(
                            "hub.ingest.records_total",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["outcome"],
                            filter_expression="signal = 'location'",
                        )
                    ],
                ),
                widget(
                    "Dependency Events by Outcome",
                    [
                        metric_query(
                            "hub.runtime.dependency_events_total",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["outcome"],
                        )
                    ],
                ),
                widget(
                    "Processing Count by Stage",
                    [
                        metric_query(
                            "hub.processing.duration.count",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["stage"],
                        )
                    ],
                ),
                widget(
                    "End-to-End Count by Event Kind",
                    [
                        metric_query(
                            "hub.processing.end_to_end_duration.count",
                            "A",
                            space_aggregation="sum",
                            time_aggregation="rate",
                            group_by=["event_kind"],
                        )
                    ],
                ),
            ],
        ),
    ]


def upsert_dashboards(base_url: str, token: str) -> list[dict]:
    auth_headers = {"Authorization": f"Bearer {token}"}
    headers = {**auth_headers, "Content-Type": "application/json"}
    existing = http_json(f"{base_url}/api/v1/dashboards", headers=auth_headers).get("data") or []
    by_title = {((item.get("data") or {}).get("title")): item for item in existing}
    results = []
    for item in desired_dashboards():
        current = by_title.get(item["title"])
        if current:
            http_json(
                f"{base_url}/api/v1/dashboards/{current['id']}",
                method="DELETE",
                headers=auth_headers,
            )
        response = http_json(
            f"{base_url}/api/v1/dashboards",
            method="POST",
            headers=headers,
            body=item,
        )
        data = response.get("data") or {}
        results.append({"title": item["title"], "id": data.get("id")})
    return results


def main() -> int:
    args = parse_args()
    try:
        token = signoz_token(args.base_url.rstrip("/"), args.email, args.password)
        dashboards = upsert_dashboards(args.base_url.rstrip("/"), token)
    except Exception as exc:  # pragma: no cover - exercised through start_demo.sh
        print(f"failed to bootstrap SigNoz dashboards: {exc}", file=sys.stderr)
        return 1

    for item in dashboards:
        print(f"bootstrapped dashboard: {item['title']} ({item['id']})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
