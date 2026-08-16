#!/usr/bin/env bash
set -Eeuo pipefail

ensure_writable_directory() {
  local path="$1"
  local mode="$2"
  local uid
  local gid

  uid="$(id -u)"
  gid="$(id -g)"

  sudo mkdir -p "$path"
  sudo chown -R "${uid}:${gid}" "$path"
  chmod "$mode" "$path"
}

ensure_writable_directory "${HOME}/.cache/go-build" 0755
ensure_writable_directory "/go/pkg/mod" 0755
ensure_writable_directory "/go/pkg/sumdb" 0755

make bootstrap

printf '\nBatchScope development environment ready.\n'
printf '  Go:      '; go version
printf '  SQLite:  '; sqlite3 --version
printf '  Codex:   '; codex --version
printf '  Claude:  '; claude --version
printf '  GitHub:  '; gh --version | head -n 1
printf '\nAgent CLIs, credential volumes, and the agent-skills Plugin are provided by agentic-development-toolkit/agent-dev.\n'
