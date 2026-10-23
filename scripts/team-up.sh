#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
bash scripts/compose-env.sh
bash scripts/compose-preflight.sh
compose=(docker compose --env-file deploy/compose/.env -f deploy/compose/docker-compose.yml -f team/mine/compose.yml)
# Stop the independent example publisher, if an earlier example session left it running.
example_id=$(docker ps -q --filter label=com.docker.compose.project=openegiz --filter label=com.docker.compose.service=mine-simulator)
if [[ -n "$example_id" ]]; then docker stop "$example_id"; fi
"${compose[@]}" up -d --build --wait --wait-timeout 900
printf '\nДиспетчерская: http://localhost:%s\n' "${MINE_PORT:-8088}"
printf 'Смена подготовлена. Назначьте маршруты и нажмите «Начать смену».\n'
printf 'Остановка с сохранением: bash scripts/team-down.sh\n'
