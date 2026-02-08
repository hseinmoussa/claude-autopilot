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
export MOCK_CLAUDE_MODE="rate_limit_once"
export MOCK_CLAUDE_STATE_DIR="${tmp_root}/mock-state"

"${BIN}" add "Global smoke task" --dir "${workdir}" --id global-smoke --priority 1 >/dev/null

cat > "${workdir}/.autopilot/tasks/project-smoke.yaml" <<YAML
id: project-smoke
priority: 2
working_dir: ${workdir}
prompt: |
  Project-local smoke task
YAML

list_out="$("${BIN}" list --project-dir "${workdir}")"
printf '%s\n' "${list_out}" | grep -q "global-smoke"
printf '%s\n' "${list_out}" | grep -q "project-smoke"

"${BIN}" run --yes --project-dir "${workdir}" >/tmp/claude-autopilot-smoke-run.log 2>&1

state_dir="${HOME}/.claude-autopilot/state"
grep -q '"status": "done"' "${state_dir}/global-smoke.state.json"
grep -q '"status": "done"' "${state_dir}/project-smoke.state.json"
grep -q '"attempt": 2' "${state_dir}/global-smoke.state.json"

status_out="$("${BIN}" status --project-dir "${workdir}")"
printf '%s\n' "${status_out}" | grep -q "Done:      2"

clean_out="$("${BIN}" clean --project-dir "${workdir}")"
printf '%s\n' "${clean_out}" | grep -q "Cleaned artifacts:"

echo "Smoke test passed"
