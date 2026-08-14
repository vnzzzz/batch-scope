#!/usr/bin/env bash
set -Eeuo pipefail

PLUGIN_NAME="agent-skills"
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

codex_marketplace_json="$(codex "${codex_marketplace_args[@]}")"
printf '%s\n' "$codex_marketplace_json"
MARKETPLACE_NAME="$(node -e 'const data=JSON.parse(process.argv[1]); process.stdout.write(data.marketplaceName)' "$codex_marketplace_json")"
PLUGIN_ID="${PLUGIN_NAME}@${MARKETPLACE_NAME}"

codex plugin marketplace upgrade "$MARKETPLACE_NAME" --json
codex plugin remove "$PLUGIN_ID" --json >/dev/null 2>&1 || true
codex plugin add "$PLUGIN_ID" --json

if ! claude plugin marketplace update "$MARKETPLACE_NAME" >/dev/null 2>&1; then
  claude plugin marketplace add "$claude_marketplace_source" --scope user
fi
if ! claude plugin update "$PLUGIN_ID" --scope user >/dev/null 2>&1; then
  claude plugin install "$PLUGIN_ID" --scope user
fi

codex plugin list --json
claude plugin list --json
