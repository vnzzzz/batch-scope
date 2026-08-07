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

prepare_persistent_directories() {
  ensure_writable_directory "${CODEX_HOME:-${HOME}/.codex}" 0700
  ensure_writable_directory "${HOME}/.claude" 0700
  ensure_writable_directory "${HOME}/.config/gh" 0700
  ensure_writable_directory "${HOME}/.cache/go-build" 0755
  ensure_writable_directory "/go/pkg/mod" 0755

  mkdir -p "${CODEX_HOME:-${HOME}/.codex}/tmp"
}

install_npm_cli() {
  local package="$1"
  local version="$2"
  local command="$3"

  if command -v "$command" >/dev/null 2>&1; then
    printf '%s already installed: ' "$command"
    "$command" --version || true
    return
  fi

  sudo npm install --global "${package}@${version}"
}

prepare_persistent_directories

install_npm_cli "@openai/codex" "${CODEX_VERSION:-latest}" codex
install_npm_cli "@anthropic-ai/claude-code" "${CLAUDE_CODE_VERSION:-latest}" claude

make bootstrap

printf '\nDevelopment environment ready.\n'
printf '  Go:     '; go version
printf '  Node:   '; node --version
printf '  Codex:  '; codex --version || true
printf '  Claude: '; claude --version || true
printf '\nRun `codex` or `claude` once to authenticate. Credentials are kept in repository-scoped named volumes.\n'
