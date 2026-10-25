#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
bash scripts/compose-env.sh
bash scripts/compose-preflight.sh
compose=(docker compose --env-file deploy/compose/.env -f deploy/compose/docker-compose.yml -f team/mine/compose.yml)
# Stop the independent example publisher, if an earlier example session left it running.
example_id=$(docker ps -q --filter label=com.docker.compose.project=openegiz --filter label=com.docker.compose.service=mine-simulator)
if [[ -n "$example_id" ]]; then docker stop "$example_id"; fi
docker_memory=$(docker info --format '{{.MemTotal}}')
low_memory=0
if [[ "${TEAM_LOW_MEMORY:-0}" == 1 || "$docker_memory" -lt 6442450944 ]]; then
  low_memory=1
  printf 'Последовательный запуск платформы для ограниченной памяти.\n'
  # Keep the two initial JVMs responsive enough to discover each other. Start
  # the remaining members after their predecessors have opened management HTTP.
  "${compose[@]}" stop mine-engine mine-web
  "${compose[@]}" stop init gateway connectivity things-search policies things extended-api grafana telegraf
  # Compile with the JVMs stopped: otherwise the compiler and Ditto compete
  # for RAM and cause sustained swapping on an 8 GB workstation.
  "${compose[@]}" build
  "${compose[@]}" up -d --wait --wait-timeout 300 mongodb mosquitto influxdb
  "${compose[@]}" up -d --wait --wait-timeout 600 policies things
  for service in connectivity things-search gateway; do
    "${compose[@]}" up -d --wait --wait-timeout 600 "$service"
  done
  # A stopped/restarted cluster can receive different Docker IPs. Nginx
  # resolves the gateway upstream at startup; recreate it after the gateway
  # is healthy so setup and dispatch never target the old address.
  "${compose[@]}" up -d --no-deps --force-recreate ditto-nginx
else
  "${compose[@]}" build
fi
up_options=(-d --wait --wait-timeout 1200)
if [[ "$low_memory" == 1 && "${TEAM_AUXILIARY:-0}" != 1 ]]; then
  # Keep the complete simulation, twin store and history. The optional
  # management panels can exhaust the remaining host memory while starting.
  up_options+=(--scale grafana=0 --scale extended-api=0)
  printf 'Режим экономии памяти: панели Grafana и OpenEgiz API выключены.\n'
  printf 'Для полного стенда: TEAM_AUXILIARY=1 bash scripts/team-up.sh\n'
fi
"${compose[@]}" up "${up_options[@]}"
printf '\nДиспетчерская: http://127.0.0.1:%s\n' "${MINE_PORT:-8088}"
printf 'Смена подготовлена. Назначьте маршруты и нажмите «Начать смену».\n'
printf 'Остановка с сохранением: bash scripts/team-down.sh\n'
