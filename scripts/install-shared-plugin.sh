#!/usr/bin/env bash
set -Eeuo pipefail

MARKETPLACE_NAME="agent-skills"
PLUGIN_ID="agent-skills@agent-skills"
MARKETPLACE_URL="${AGENT_SKILLS_MARKETPLACE_URL:-https://github.com/vnzzzz/agent-skills.git}"
MARKETPLACE_REF="${AGENT_SKILLS_MARKETPLACE_REF:-}"

command -v codex >/dev/null || { echo "ERROR: codex CLI is required." >&2; exit 1; }
command -v claude >/dev/null || { echo "ERROR: claude CLI is required." >&2; exit 1; }

codex_marketplace_args=(plugin marketplace add "$MARKETPLACE_URL")
claude_marketplace_source="$MARKETPLACE_URL"
if [[ -n "$MARKETPLACE_REF" ]]; then
  codex_marketplace_args+=(--ref "$MARKETPLACE_REF")
  claude_marketplace_source="${MARKETPLACE_URL}#${MARKETPLACE_REF}"
fi
codex_marketplace_args+=(--json)

codex "${codex_marketplace_args[@]}"
codex plugin marketplace upgrade "$MARKETPLACE_NAME" --json
codex plugin add "$PLUGIN_ID" --json

claude plugin marketplace add "$claude_marketplace_source" --scope user
claude plugin marketplace update "$MARKETPLACE_NAME"
claude plugin install "$PLUGIN_ID" --scope user
claude plugin update "$PLUGIN_ID" --scope user

codex plugin list --json
claude plugin list --json
