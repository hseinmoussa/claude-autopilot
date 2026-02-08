#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT}/claude-autopilot"

if [[ ! -x "${BIN}" ]]; then
  (cd "${ROOT}" && go build -o "${BIN}" .)
fi

tmp_root="$(mktemp -d)"
trap 'rm -rf "${tmp_root}"' EXIT

export HOME="${tmp_root}/home"
workdir="${tmp_root}/project"
mock_bin_dir="${tmp_root}/mock-bin"
mkdir -p "${HOME}" "${workdir}" "${mock_bin_dir}" "${workdir}/.autopilot/tasks"
ln -sf "${ROOT}/test/mock_claude.sh" "${mock_bin_dir}/claude"
export PATH="${mock_bin_dir}:${PATH}"
export MOCK_CLAUDE_STATE_DIR="${tmp_root}/mock-state"

run_and_assert_done() {
  local task_id="$1"
  "${BIN}" run --yes --project-dir "${workdir}" >/dev/null
  grep -q '"status": "done"' "${HOME}/.claude-autopilot/state/${task_id}.state.json"
}

# 1) old-version compatibility mode (no stream-json)
export MOCK_CLAUDE_VERSION="1.5.0"
export MOCK_CLAUDE_MODE="success"
"${BIN}" add "Old version compatibility task" --dir "${workdir}" --id old-version --priority 1 >/dev/null
run_and_assert_done "old-version"

# 2) rate limit once then resume
export MOCK_CLAUDE_VERSION="2.1.0"
export MOCK_CLAUDE_MODE="rate_limit_once"
"${BIN}" add "Rate limit once task" --dir "${workdir}" --id rl-once --priority 1 >/dev/null
run_and_assert_done "rl-once"
grep -q '"attempt": 2' "${HOME}/.claude-autopilot/state/rl-once.state.json"

# 3) graceful shutdown with SIGTERM
export MOCK_CLAUDE_MODE="long_running"
"${BIN}" add "Graceful shutdown task" --dir "${workdir}" --id graceful --priority 1 >/dev/null
set +e
"${BIN}" run --yes --project-dir "${workdir}" >/tmp/claude-autopilot-int-shutdown.log 2>&1 &
runner_pid=$!
sleep 2
kill -TERM "${runner_pid}"
wait "${runner_pid}"
exit_code=$?
set -e

if [[ "${exit_code}" -ne 130 ]]; then
  echo "expected exit code 130, got ${exit_code}" >&2
  exit 1
fi

grep -q '"status": "pending"' "${HOME}/.claude-autopilot/state/graceful.state.json"
"${BIN}" cancel graceful --project-dir "${workdir}" >/dev/null || true

# 4) cancel while run is active (queued control command)
export MOCK_CLAUDE_MODE="rate_limit"
"${BIN}" add "Queued cancel task" --dir "${workdir}" --id cancel-while-active --priority 1 >/dev/null
"${BIN}" run --yes --project-dir "${workdir}" >/tmp/claude-autopilot-int-cancel.log 2>&1 &
runner_pid=$!
sleep 2
cancel_out="$("${BIN}" cancel cancel-while-active --project-dir "${workdir}")"
echo "${cancel_out}" | grep -q "Queued cancel for cancel-while-active"

# cancel should be applied on the next wait tick (<= 30s)
for _ in $(seq 1 40); do
  if ! kill -0 "${runner_pid}" 2>/dev/null; then
    break
  fi
  sleep 1
done

if kill -0 "${runner_pid}" 2>/dev/null; then
  echo "runner did not exit after queued cancel" >&2
  kill -9 "${runner_pid}" || true
  exit 1
fi

grep -q '"status": "cancelled"' "${HOME}/.claude-autopilot/state/cancel-while-active.state.json"

echo "Integration tests passed"
