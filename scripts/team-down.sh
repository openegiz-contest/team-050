#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
docker compose --env-file deploy/compose/.env -f deploy/compose/docker-compose.yml -f team/mine/compose.yml down
