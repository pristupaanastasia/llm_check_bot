#!/usr/bin/env bash
# Thermal guard: pauses the bot's LLM checks while the Mac is overheating.
#
# Runs on the Mac itself (not in Docker: containers can't see the Mac's
# sensors). Every INTERVAL seconds it reads macOS's thermal state
# (0 nominal, 1 fair, 2 serious, 3 critical; no sudo needed):
#   - state >= PAUSE_AT (default 1, fair) → sets llm:paused in Redis (the bot stops sending
#                          messages to the model) and unloads the model;
#   - state <= RESUME_AT → removes the flag, the bot resumes.
# The flag has a TTL, so if this script dies the bot resumes on its own.
#
# Usage:  ./scripts/thermal-guard.sh            # loop
#         ./scripts/thermal-guard.sh --status   # print state once
set -euo pipefail

INTERVAL="${INTERVAL:-30}"
PAUSE_AT="${PAUSE_AT:-1}"
RESUME_AT="${RESUME_AT:-0}"
OLLAMA_URL="${OLLAMA_URL:-http://localhost:11434}"
OLLAMA_MODEL="${OLLAMA_MODEL:-gemma4:e2b-mlx}"
KEY="llm:paused"
TTL=$(( INTERVAL * 4 ))

cd "$(dirname "$0")/.."
names=(нормально повышенная серьёзная критическая)

thermal_state() {
  osascript -l JavaScript -e 'ObjC.import("Foundation"); $.NSProcessInfo.processInfo.thermalState'
}

redis() { docker compose exec -T redis redis-cli "$@" >/dev/null; }

log() { echo "$(date '+%F %T') $*"; }

if [[ "${1:-}" == "--status" ]]; then
  s=$(thermal_state)
  paused=$(docker compose exec -T redis redis-cli EXISTS "$KEY" 2>/dev/null || echo "?")
  echo "температура: ${names[$s]} ($s), модель на паузе: $([[ $paused == 1 ]] && echo да || echo нет)"
  exit 0
fi

log "сторож запущен: пауза при состоянии >= $PAUSE_AT, возврат при <= $RESUME_AT, проверка раз в ${INTERVAL}с"
paused=0
while true; do
  s=$(thermal_state)
  if (( s >= PAUSE_AT )); then
    redis SET "$KEY" "$s" EX "$TTL" || log "не удалось записать флаг в Redis"
    if (( paused == 0 )); then
      log "🔥 перегрев: ${names[$s]} ($s) — модель на паузе"
      curl -s -m 5 "$OLLAMA_URL/api/generate" \
        -d "{\"model\":\"$OLLAMA_MODEL\",\"keep_alive\":0}" >/dev/null || true
      paused=1
    fi
  elif (( paused == 1 && s <= RESUME_AT )); then
    redis DEL "$KEY" || log "не удалось снять флаг в Redis"
    log "✅ остыл: ${names[$s]} ($s) — модель снова работает"
    paused=0
  elif (( paused == 1 )); then
    redis SET "$KEY" "$s" EX "$TTL" || true # still warm: keep the pause alive
  fi
  sleep "$INTERVAL"
done
