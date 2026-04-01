# Connectors

This directory contains demonstrator and integration projects that feed data
into a locally running Open RTLS Hub without changing the hub itself.

Connector projects in this repository should:

- stay environment-driven and runnable outside the hub process
- document their upstream data sources and any source-specific limitations
- prefer the hub's existing OMLOX interfaces over private integration paths
- keep bootstrap utilities and runtime connectors in the same project when they
  depend on the same upstream metadata

The first demonstrator is [`connectors/gtfs`](/Users/jillesvangurp/git/open-rtls/open-rtls-hub/connectors/gtfs),
which forwards GTFS-RT vehicle updates to the hub over WebSocket and bootstraps
station zones and fence polygons from GTFS stop geometry plus optional external
reference datasets.
