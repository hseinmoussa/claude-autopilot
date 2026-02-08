#!/usr/bin/env bash
set -euo pipefail

mode="${MOCK_CLAUDE_MODE:-success}"
state_dir="${MOCK_CLAUDE_STATE_DIR:-/tmp}"
version="${MOCK_CLAUDE_VERSION:-2.1.0}"
mkdir -p "${state_dir}"

next_minute() {
  python3 - <<'PY'
from datetime import datetime, timedelta
print((datetime.now() + timedelta(minutes=1)).strftime("%I:%M %p").lstrip("0"))
PY
}

if [[ "${1:-}" == "--version" ]]; then
  echo "claude ${version}"
  exit 0
fi

case "${mode}" in
  success)
    printf '{"type":"system","session_id":"mock-session"}\n'
    printf '{"type":"assistant","message":"working"}\n'
    printf '{"type":"result"}\n'
    ;;
  fail)
    printf '{"type":"system","session_id":"mock-session"}\n'
    printf '{"type":"assistant","message":"failed"}\n'
    >&2 echo "mock failure"
    exit 1
    ;;
  rate_limit)
    printf '{"type":"system","session_id":"mock-session"}\n'
    >&2 echo "rate limit exceeded. reset at $(next_minute)."
    exit 75
    ;;
  rate_limit_once)
    marker="${state_dir}/mock-rate-limit-once.marker"
    if [[ ! -f "${marker}" ]]; then
      touch "${marker}"
      printf '{"type":"system","session_id":"mock-session"}\n'
      >&2 echo "rate limit exceeded. reset at $(next_minute)."
      exit 75
    fi
    printf '{"type":"system","session_id":"mock-session"}\n'
    printf '{"type":"assistant","message":"resumed"}\n'
    printf '{"type":"result"}\n'
    ;;
  long_running)
    trap 'exit 143' TERM INT
    printf '{"type":"system","session_id":"mock-session"}\n'
    while true; do
      printf '{"type":"assistant","message":"still working"}\n'
      sleep 2
    done
    ;;
  *)
    >&2 echo "unknown MOCK_CLAUDE_MODE=${mode}"
    exit 2
    ;;
esac
