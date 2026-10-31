#!/usr/bin/env bash
# Creates the least-privilege roles used by the services.
#   trackadoodle_app    - backend: reads/writes users and activities
#   trackadoodle_martin - tile server: may only EXECUTE the tile function
set -euo pipefail

: "${APP_DB_PASSWORD:?APP_DB_PASSWORD must be set}"
: "${MARTIN_DB_PASSWORD:?MARTIN_DB_PASSWORD must be set}"

psql -v ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v app_pw="$APP_DB_PASSWORD" -v martin_pw="$MARTIN_DB_PASSWORD" <<'SQL'
CREATE ROLE trackadoodle_app    LOGIN PASSWORD :'app_pw';
CREATE ROLE trackadoodle_martin LOGIN PASSWORD :'martin_pw';
SQL
