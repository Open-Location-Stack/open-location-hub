# Connectors

This directory contains demonstrator and integration projects that feed data
into a locally running Open RTLS Hub without changing the hub itself.

Connector projects in this repository should:

- stay environment-driven and runnable outside the hub process
- document their upstream data sources and any source-specific limitations
- prefer the hub's existing OMLOX interfaces over private integration paths
- keep bootstrap utilities and runtime connectors in the same project when they
  depend on the same upstream metadata
- reuse the shared local hub runtime under [`connectors/local-hub`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/local-hub) when they need a local demo stack

Available connector demos:

- [`connectors/local-hub`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/local-hub): reusable local hub, Postgres, Dex, and Mosquitto stack
- [`connectors/gtfs`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs): GTFS-RT vehicle updates and station fence bootstrap
- [`connectors/opensky`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/opensky): OpenSky aircraft positions with airport-sector fences
