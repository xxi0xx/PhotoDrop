#!/bin/sh
set -eu

# Docker creates new host bind-mount directories as root on Linux. Prepare
# only the configured directory, then replace the shell with the app as 10001.
# Existing files are never recursively chowned. --user skips this preparation.
if [ "$(id -u)" = "0" ]; then
    data_dir=${PHOTODROP_DATA_DIR:-/data}
    mkdir -p -- "$data_dir"
    chown photodrop:photodrop -- "$data_dir"
    chmod 0700 -- "$data_dir"
    exec su-exec photodrop:photodrop "$@"
fi

exec "$@"
