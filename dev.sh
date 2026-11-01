#!/usr/bin/env bash
# Development helper for the track-a-doodle stack. Run ./dev.sh help for usage.
set -euo pipefail

cd "$(dirname "$0")"

FRONTEND_URL="http://localhost:5173"
BACKEND_URL="http://localhost:8080"
# The network with the database; see compose.yaml.
COMPOSE_NETWORK="track-a-doodle_data"
# Go module cache shared by test runs, so dependencies are downloaded once.
GO_CACHE_VOLUME="track-a-doodle-go-cache"
TEST_DATABASE_URL="postgres://trackadoodle_app:${APP_DB_PASSWORD:-dev-app-pw}@db:5432/trackadoodle"

usage() {
  cat <<'EOF'
Usage: ./dev.sh [command]

  up         Rebuild all modules and (re)create every container (default).
             Keeps the database. Run this after pulling, switching branches,
             or changing dependencies.
  down       Stop the stack. Keeps the database.
  status     Show the containers.
  logs [s]   Follow logs, optionally for given services (db, martin, backend, frontend).
  test       Run backend tests (including database tests) and the frontend type check.
  import [dir] [-allow-duplicates]
             Batch-import GPX files from dir (default samples/), searched
             recursively, e.g. ./dev.sh import ~/gpx
  reset-db   Delete ALL data and recreate the database from postgis/init.
             Needed after changing the SQL there. Asks for confirmation.
  help       Show this help.
EOF
}

log() { printf '\033[1m==> %s\033[0m\n' "$*"; }

require_docker() {
  if ! docker info >/dev/null 2>&1; then
    echo "Docker is not running. Start Docker Desktop and try again." >&2
    exit 1
  fi
}

# wait_for NAME URL: polls URL until it answers, for up to 60 seconds.
wait_for() {
  local name=$1 url=$2
  for _ in $(seq 1 60); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then
      echo "    $name is up: $url"
      return 0
    fi
    sleep 1
  done
  echo "    $name did not come up at $url. Recent logs:" >&2
  docker compose logs --tail 30 >&2
  return 1
}

cmd_up() {
  require_docker
  log "Building images (postgis, backend, frontend) and pulling martin"
  docker compose pull --quiet martin
  docker compose build
  log "Recreating containers"
  # --force-recreate replaces containers even if their config is unchanged,
  # which also refreshes bind mounts; --renew-anon-volumes gives the frontend
  # a fresh node_modules from the new image.
  docker compose up --detach --wait --force-recreate --renew-anon-volumes --remove-orphans
  log "Waiting for services"
  wait_for backend "$BACKEND_URL/api/healthz"
  wait_for frontend "$FRONTEND_URL"
  docker compose ps --format 'table {{.Service}}\t{{.Status}}\t{{.Ports}}'
  log "Ready: open $FRONTEND_URL"
}

# run_backend_tests [docker run options...]: vets and tests the backend in a Go container.
run_backend_tests() {
  docker run --rm "$@" -v "$PWD/backend":/src -v "$GO_CACHE_VOLUME":/go/pkg/mod -w /src \
    golang:1.25-alpine sh -c 'go vet ./... && go test ./...'
}

cmd_test() {
  require_docker
  log "Backend tests"
  if docker network inspect "$COMPOSE_NETWORK" >/dev/null 2>&1 \
    && [ "$(docker compose ps --status running --quiet db)" != "" ]; then
    run_backend_tests --network "$COMPOSE_NETWORK" -e TEST_DATABASE_URL="$TEST_DATABASE_URL"
  else
    echo "    The database isn't running, so database tests are skipped (run ./dev.sh up first)."
    run_backend_tests
  fi
  log "Frontend type check"
  docker compose run --rm --no-deps frontend npm run typecheck
}

# cmd_import [dir] [flags...]: imports dir (default samples/, which compose
# already mounts) by mounting it read-only into a one-off backend container.
cmd_import() {
  require_docker
  if [ $# -gt 0 ] && [ "${1#-}" = "$1" ]; then
    local dir=$1
    shift
    if [ ! -d "$dir" ]; then
      echo "Not a directory: $dir" >&2
      exit 1
    fi
    dir=$(cd "$dir" && pwd)
    log "Importing GPX files from $dir"
    docker compose run --rm -v "$dir":/import:ro backend import -dir /import "$@"
  else
    docker compose run --rm backend import "$@"
  fi
}

cmd_reset_db() {
  require_docker
  echo "This deletes ALL users and imported activities in the local database."
  read -r -p "Type 'reset' to continue: " answer
  if [ "$answer" != "reset" ]; then
    echo "Aborted."
    exit 1
  fi
  log "Removing containers and volumes"
  docker compose down --volumes --remove-orphans
  cmd_up
}

case "${1:-up}" in
  up) cmd_up ;;
  down) require_docker; docker compose down ;;
  status) require_docker; docker compose ps ;;
  logs) require_docker; shift; docker compose logs --follow --tail 100 "$@" ;;
  test) cmd_test ;;
  import) shift; cmd_import "$@" ;;
  reset-db) cmd_reset_db ;;
  help | -h | --help) usage ;;
  *)
    echo "Unknown command: $1" >&2
    usage >&2
    exit 1
    ;;
esac
