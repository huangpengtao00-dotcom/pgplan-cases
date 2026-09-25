#!/bin/sh
# Start a throwaway PostgreSQL in a temp dir and print the DSN to export:
#   eval "$(scripts/local-pg.sh)"
# Needs initdb and pg_ctl on PATH (e.g. `brew install postgresql@16`).
set -eu
dir=$(mktemp -d)
port=${PGPLAN_PORT:-$((50000 + $$ % 10000))}
initdb -D "$dir/data" -U postgres --auth=trust --no-locale -E UTF8 >/dev/null
if ! pg_ctl -D "$dir/data" -o "-p $port -k $dir" -l "$dir/log" -w start >/dev/null; then
  echo "echo 'postgres failed to start, see $dir/log' >&2; false"
  exit 1
fi
echo "export PGPLAN_DSN='postgres://postgres@localhost:$port/postgres?host=$dir'"
echo "echo 'stop with: pg_ctl -D $dir/data stop' >&2"
