# Tech stack

Status: **proposed**. This is not final yet. It comes from an earlier discussion in the claude.ai project "Exercise data GIS web application".

## Summary

| Layer | Choice | Rejected / alternatives |
|---|---|---|
| Database | PostGIS | DuckDB-spatial (only fits a single-user desktop tool); `h3-pg` for hexagon aggregates (deferred: the zoomed-out overview uses count bubbles clustered in the browser) |
| Tiles | Vector tiles (MVT) from PostGIS via **Martin**, or a backend `ST_AsMVT` endpoint | GeoServer (too heavy; built around WMS/WFS and raster), pg_tileserv |
| Frontend map | **MapLibre GL JS** + **deck.gl** | Leaflet (raster/DOM based, slow with thousands of tracks), OpenLayers (more verbose), custom WebGL (deck.gl already does it) |
| Backend | **Go** (chi, pgx, gpxgo, a FIT library); kept thin | Python FastAPI (gpxpy, fitdecode), Rust Axum, Supabase (no backend) |
| Basemaps | OpenFreeMap or self-hosted Protomaps PMTiles, plus selectable raster layers | tile.openstreetmap.org (development only; its usage policy forbids heavy app traffic) |

## Why vector tiles instead of GeoServer

The browser styles and filters MVT tiles on the GPU. Martin can serve SQL *functions* as tile sources, for example `activities_tiles(z, x, y, query_params)` with a user id, sport and date range. Alternatively, a backend endpoint runs `ST_AsMVT` and returns the bytes. That's about 30 lines of code and makes authentication trivial.

## Architecture: hybrid client and server

One person's data is small. A heavy user has about 3,000 activities; simplified to about 200 points each, that's roughly 600k points, or a few MB.

1. **Overview and filtering in the browser.** On load, fetch the user's simplified tracks plus metadata (date, sport, distance, city) as one compact file (FlatGeobuf, GeoArrow or a custom binary format). Filtering and the heatmap run on the GPU with deck.gl `DataFilterExtension`, with no server round trips.
2. **Detail from tiles.** At high zoom, MapLibre loads full-resolution MVT tiles with per-point time, pace and heart rate.

This avoids the cache problem that server-side filtering creates: a tile cache can't be reused across every user × filter combination.

Useful deck.gl layers: `DataFilterExtension` (time slider), `TripsLayer` (animated replays), `HexagonLayer` / `H3HexagonLayer` (aggregates), `PathLayer` with additive blending (line-density heatmap).

## Views by zoom level

- **z0–8:** aggregates. Reverse-geocode each activity's start point once, at import, against GeoNames or OSM admin polygons in PostGIS, and store `city_id`. Show the results as bubbles or H3 hexagons.
- **z9–12:** simplified lines plus the density heatmap.
- **z13+:** full detail; activities are clickable and colored by pace, heart rate or elevation.

At import, precompute 2–3 simplified geometries per activity in EPSG:3857, at tolerances matched to the zoom bands.

## Schema sketch

```sql
activities(
  id, user_id, sport, started_at timestamptz, duration, distance_m,
  geom       geometry(LineStringZM, 4326),  -- Z = elevation, M = epoch time
  geom_lo    geometry(LineString, 3857),    -- simplified, for overview
  geom_mid   geometry(LineString, 3857),
  start_pt   geometry(Point, 4326),
  city_id, source, raw_file_ref
)
-- GIST indexes on the geometries, btree on (user_id, started_at)
```

Storing timestamps in `M` avoids a separate points table until per-point analytics are needed. Keep the original files in object storage (S3/MinIO) so they can be re-imported.

## Import

- Formats: GPX, **FIT** (Garmin's native format; has heart rate, cadence and power), TCX.
- Bulk exports first: Strava's "download your archive" and Garmin's account data export.
- Strava API (OAuth, webhooks): check the current API agreement, which was tightened in late 2024 around showing data to other users.
- The Garmin API requires applying to their developer program, so start with file import.
- Add privacy zones early (trim the start and end of tracks near home).

## Basemaps

- Default: OpenFreeMap (vector, no API key) or a self-hosted Protomaps PMTiles file on S3 or a CDN.
- Selectable raster layers: OpenTopoMap, CyclOSM, Esri World Imagery (check its terms). With API keys: MapTiler, Stadia, Thunderforest. For Sweden: Lantmäteriet (check their open-data terms).

## Suggested MVP order

1. PostGIS + GPX/FIT import
2. `ST_AsMVT` tile endpoint
3. MapLibre with an OpenFreeMap basemap
4. Date filter
5. deck.gl heatmap
6. City aggregation
7. Strava sync
