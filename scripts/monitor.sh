#!/usr/bin/env bash
# Probe the configured health URL and optionally send a transition alert via ntfy.
# This detects application failures while the host is alive. For a full
# host or network outage, pair it with an external monitor (UptimeRobot, Better
# Stack, Cloudflare Health Checks, etc.).
set -u

URL="${P2611003_HEALTH_URL:-http://127.0.0.1:8080/healthz}"
NTFY="${P2611003_NTFY_URL:-}"
STATE_DIR="${P2611003_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/p2611003-monitor}"
STATE_FILE="$STATE_DIR/state"
FAIL_FILE="$STATE_DIR/fails"

mkdir -p "$STATE_DIR"
fails=0
[ -f "$FAIL_FILE" ] && fails=$(cat "$FAIL_FILE" 2>/dev/null || echo 0)
previous="up"
[ -f "$STATE_FILE" ] && previous=$(cat "$STATE_FILE" 2>/dev/null || echo up)

notify() {
  local title="$1" message="$2"
  [ -n "$NTFY" ] || return 0
  curl -fsS --max-time 10 \
    -H "Title: $title" \
    -H "Priority: high" \
    -H "Tags: rotating_light" \
    -d "$message" "$NTFY" >/dev/null 2>&1 || true
}

up() {
  local body
  body=$(curl -fsS --max-time 20 --retry 1 "$URL" 2>/dev/null) || return 1
  printf '%s' "$body" | grep -q '"ok":true'
}

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if up; then
  printf '0' > "$FAIL_FILE"
  if [ "$previous" = "down" ]; then
    printf 'up' > "$STATE_FILE"
    notify "Report for public recovered" "$URL is responding again at $now."
    echo "$now UP (recovered)"
  else
    printf 'up' > "$STATE_FILE"
    echo "$now UP"
  fi
else
  fails=$((fails + 1))
  printf '%s' "$fails" > "$FAIL_FILE"
  if [ "$fails" -ge 3 ] && [ "$previous" != "down" ]; then
    printf 'down' > "$STATE_FILE"
    notify "Report for public is DOWN" "$URL failed 3 consecutive checks. Last check: $now."
    echo "$now DOWN (alert sent, failures=$fails)"
  else
    echo "$now FAIL (failures=$fails, threshold=3)"
  fi
fi
