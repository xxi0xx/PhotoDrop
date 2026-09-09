#!/bin/sh
# Run from the repository root. Leaves the stopped container and ./data for
# inspection; use docker compose down afterward. Does not remove persistent data.
set -eu

docker compose up --build --wait --wait-timeout 120 -d
curl --fail --silent --show-error http://localhost:8080/healthz | grep -q '"status":"ok"'
curl --fail --silent --show-error http://localhost:8080/ | grep -q '<title>PhotoDrop</title>'

container=$(docker compose ps -q photodrop)
docker compose exec -T photodrop sh -ec '
    test -s /data/photodrop.db
    test "$(awk "/^Uid:/ {print \$2}" /proc/1/status)" = 10001
    for tool in node npm go; do
        if command -v "$tool"; then
            echo "Unexpected build tool in runtime: $tool" >&2
            exit 1
        fi
    done
'
docker compose stop
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
docker compose logs --no-color | grep -q 'HTTP server stopped'
# Hash the stopped SQLite file so a restart must leave all bookkeeping intact.
before=$(docker compose run --rm --no-deps --entrypoint sha256sum photodrop /data/photodrop.db)
docker compose up --wait --wait-timeout 120 -d
# Compose may recreate the service container after the one-off checksum run.
# Inspect the currently running container rather than its pre-restart ID.
container=$(docker compose ps -q photodrop)
curl --fail --silent --show-error http://localhost:8080/healthz >/dev/null
docker compose stop
after=$(docker compose run --rm --no-deps --entrypoint sha256sum photodrop /data/photodrop.db)
test "$before" = "$after"
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
printf '%s\n' 'Compose smoke checks passed: HTTP, non-root app, persistence, restart, SIGTERM, minimal runtime.'
