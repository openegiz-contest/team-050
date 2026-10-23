#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
bash scripts/compose-env.sh
bash scripts/compose-preflight.sh
compose=(docker compose --env-file deploy/compose/.env -f deploy/compose/docker-compose.yml -f team/mine/compose.yml)
# Stop the independent example publisher, if an earlier example session left it running.
example_id=$(docker ps -q --filter label=com.docker.compose.project=openegiz --filter label=com.docker.compose.service=mine-simulator)
if [[ -n "$example_id" ]]; then docker stop "$example_id"; fi
"${compose[@]}" build
docker_memory=$(docker info --format '{{.MemTotal}}')
if [[ "${TEAM_LOW_MEMORY:-0}" == 1 || "$docker_memory" -lt 6442450944 ]]; then
  printf 'Последовательный запуск платформы для ограниченной памяти.\n'
  # Keep the two initial JVMs responsive enough to discover each other. Start
  # the remaining members after their predecessors have opened management HTTP.
  "${compose[@]}" stop mine-engine mine-web
  "${compose[@]}" stop init gateway connectivity things-search policies things extended-api grafana telegraf
  "${compose[@]}" up -d --wait --wait-timeout 300 mongodb mosquitto influxdb
  "${compose[@]}" up -d --wait --wait-timeout 600 policies things
  for service in connectivity things-search gateway; do
    "${compose[@]}" up -d --wait --wait-timeout 600 "$service"
  done
fi
"${compose[@]}" up -d --wait --wait-timeout 1200
printf '\nДиспетчерская: http://localhost:%s\n' "${MINE_PORT:-8088}"
printf 'Смена подготовлена. Назначьте маршруты и нажмите «Начать смену».\n'
printf 'Остановка с сохранением: bash scripts/team-down.sh\n'
