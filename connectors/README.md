# Connectors

This directory contains demonstrator and integration projects that feed data
into a locally running Open RTLS Hub without changing the hub itself.

Connector projects in this repository should:

- stay environment-driven and runnable outside the hub process
- document their upstream data sources and any source-specific limitations
- prefer the hub's existing OMLOX interfaces over private integration paths
- keep bootstrap utilities and runtime connectors in the same project when they
  depend on the same upstream metadata
- reuse the shared local hub runtime under [`connectors/local-hub`](local-hub) when they need a local demo stack

Available connector demos:

- [`connectors/local-hub`](local-hub): reusable local hub, Postgres, Dex, and Mosquitto stack
- [`connectors/gtfs`](gtfs): GTFS-RT vehicle updates and station fence bootstrap
- [`connectors/opensky`](opensky): OpenSky aircraft positions with airport-sector fences
- [`connectors/replay`](replay): diagnostic NDJSON trace replay with timestamp correction, acceleration, and interpolation
