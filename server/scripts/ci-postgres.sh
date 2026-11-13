#!/usr/bin/env bash
# Starts a throwaway PostgreSQL 16 as the current (non-root) user on
# localhost:5432 with one password-authenticated superuser and database.
# CI jobs use it instead of a services: container so they need no Docker.
#
# Usage: ci-postgres.sh <user> <password> <database>
set -euo pipefail

user=${1:?usage: ci-postgres.sh <user> <password> <database>}
password=${2:?usage: ci-postgres.sh <user> <password> <database>}
database=${3:?usage: ci-postgres.sh <user> <password> <database>}

pg_bin=${PG_BIN:-/usr/lib/postgresql/16/bin}
root=${RUNNER_TEMP:-$(mktemp -d)}/postgres
data=$root/data

mkdir -p "$root"
pwfile=$root/pwfile
printf '%s\n' "$password" >"$pwfile"
"$pg_bin/initdb" -D "$data" -U "$user" --pwfile="$pwfile" \
  --auth=scram-sha-256 --encoding=UTF8 --locale=C.UTF-8 >/dev/null
rm -f "$pwfile"

"$pg_bin/pg_ctl" -D "$data" -l "$root/postgres.log" -w -t 60 \
  -o "-c listen_addresses=localhost -c port=5432 -k $root" start

PGPASSWORD=$password "$pg_bin/createdb" -h localhost -p 5432 -U "$user" "$database"
echo "postgres ready: postgres://$user@localhost:5432/$database (log: $root/postgres.log)"
