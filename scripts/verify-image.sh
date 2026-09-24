#!/bin/sh
# Disposable runtime inspection; never mounts operator state.
set -eu
image=$1 version=$2 revision=$3 platform=${4:-linux/amd64}
test "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" = "$version"
test "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$revision"
test "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.source"}}')" = 'https://github.com/xxi0xx/PhotoDrop'
test "$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.title"}}')" = PhotoDrop
test "$(docker run --rm --platform "$platform" "$image" photodrop version)" = "PhotoDrop $version ($revision)"
password=$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')
container=$(docker run -d --platform "$platform" -e PHOTODROP_ADMIN_PASSWORD="$password" "$image")
trap 'docker rm -fv "$container" >/dev/null' EXIT
for attempt in $(seq 1 60); do
  state=$(docker inspect --format '{{.State.Health.Status}}' "$container")
  [ "$state" != healthy ] || break
  sleep 2
done
test "$state" = healthy
docker exec "$container" sh -ec '
  test "$(awk "/^Uid:/ {print \$2}" /proc/1/status)" = 10001
  test "$(stat -c %a /data)" = 700
  for tool in go node npm gcc; do ! command -v "$tool"; done
  test ! -e /src && test ! -e /.git && test ! -e /.env
  test ! -e /scripts && test ! -e /internal
  test -x /usr/local/bin/photodrop
'
docker stop --time 15 "$container" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 0
docker logs "$container" 2>&1 | grep -q 'HTTP server stopped'
echo "Runtime verified: $platform, version, health, UID 10001, private /data, minimal image, SIGTERM."
