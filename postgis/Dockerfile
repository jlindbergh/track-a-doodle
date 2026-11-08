# PostgreSQL + PostGIS.
# Built from the official postgres image because postgis/postgis has no arm64 builds.
FROM postgres:17-bookworm

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      postgresql-17-postgis-3 \
      postgresql-17-postgis-3-scripts \
 && rm -rf /var/lib/apt/lists/*

# Only the roles are created here, when the volume is first created. The
# schema comes from migrations/, applied by the migrate service.
COPY init/ /docker-entrypoint-initdb.d/
