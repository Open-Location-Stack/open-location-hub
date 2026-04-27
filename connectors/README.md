# Connectors

This directory contains demonstrator and integration projects that feed data
into a locally running Open RTLS Hub without changing the hub itself.

If you are new to the repository and want to run the hub on your laptop first,
start with [`local-hub/README.md`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub/README.md)
before coming back to these connector examples.

Connector projects in this repository should:

- stay environment-driven and runnable outside the hub process
- document their upstream data sources and any source-specific limitations
- prefer the hub's existing OMLOX interfaces over private integration paths
- keep bootstrap utilities and runtime connectors in the same project when they
  depend on the same upstream metadata
- reuse the shared local runtime under [`local-hub/`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/local-hub) when they need a local demo stack

Available connector demos:

- [`connectors/gtfs`](gtfs): GTFS-RT vehicle updates and station fence bootstrap
- [`connectors/opensky`](opensky): OpenSky aircraft positions with airport-sector fences
- [`connectors/replay`](replay): diagnostic NDJSON trace replay with timestamp correction, acceleration, and interpolation
- [`connectors/uwb_sim`](uwb_sim): mock 3-floor UWB building simulator with georeferenced floorplan assets and WGS84 location ingest
