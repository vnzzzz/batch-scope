#!/usr/bin/env bash
set -Eeuo pipefail

MARKETPLACE_SOURCE=${AGENT_SKILLS_MARKETPLACE_SOURCE:-https://github.com/vnzzzz/agent-skills.git}
MARKETPLACE_NAME=vnzzzz-agent-skills
PLUGIN_ID=agent-skills@vnzzzz-agent-skills

command -v codex >/dev/null || {
  echo "ERROR: codex CLI is required before installing agent-skills Plugin." >&2
  exit 1
}
command -v claude >/dev/null || {
  echo "ERROR: claude CLI is required before installing agent-skills Plugin." >&2
  exit 1
}

codex plugin remove "$PLUGIN_ID" --json >/dev/null 2>&1 || true
codex plugin marketplace remove "$MARKETPLACE_NAME" --json >/dev/null 2>&1 || true
codex plugin marketplace add "$MARKETPLACE_SOURCE" --json >/dev/null
codex plugin add "$PLUGIN_ID" --json >/dev/null

claude plugin uninstall "$PLUGIN_ID" --scope user >/dev/null 2>&1 || true
claude plugin marketplace remove "$MARKETPLACE_NAME" >/dev/null 2>&1 || true
claude plugin marketplace add "$MARKETPLACE_SOURCE" --scope user >/dev/null
claude plugin install "$PLUGIN_ID" --scope user >/dev/null

printf 'agent-skills Plugin installed for Codex and Claude Code from %s.\n' "$MARKETPLACE_SOURCE"
