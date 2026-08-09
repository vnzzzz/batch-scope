#!/usr/bin/env bash
set -Eeuo pipefail

input="${1:-/dev/stdin}"

if ! command -v jq >/dev/null 2>&1; then
  echo 'jqが必要です。Dev Container内で実行してください。' >&2
  exit 1
fi

jq -r '
  def node_label:
    "\(.id)  \(.name)";

  def limit_label:
    "    \(.limitOwner.id)  \(.limitOwner.name)  \(.fact.sourceText // .fact.duration // .fact.kind)" +
    (if .scopeRoot != null then "  スコープルート=\(.scopeRoot.id)" else "" end) +
    "  確実性=\(.fact.certainty)";

  def limit_section($title; $limits):
    $title,
    "  終了時刻リミット",
    (if ($limits.finishByGroups | length) == 0 then "    なし"
     else ($limits.finishByGroups[] |
       "    タイムゾーン: \(.timeZone)  件数=\(.total)",
       (if (.items | length) == 0 then "      なし" else (.items[] | limit_label) end)) end),
    "  経過時間リミット",
    (if ($limits.maxElapsed.items | length) == 0 then "    なし"
     else ($limits.maxElapsed.items[] | limit_label) end),
    "  元設定のリミット",
    (if ($limits.raw.items | length) == 0 then "    なし"
     else ($limits.raw.items[] | limit_label) end);

  def relation_label:
    "\(.kind) / \(.origin) / \(.certainty)";

  def via_relations:
    if ((.viaRelations // []) | length) == 0 then ""
    else "  <- " + ([.viaRelations[] | relation_label] | join(", "))
    end;

  def tree_lines($indent):
    ($indent + (.node | node_label) + via_relations +
      (if .referenceType == "cycle" then "  [循環]"
       elif .referenceType == "shared" then "  [合流]"
       else "" end) +
      (if (.hiddenJobCount // 0) > 0 then "  [\(.hiddenJobCount)件省略]" else "" end)),
    ((.children // [])[] | tree_lines($indent + "  "));

  "対象: \(.target.id)  \(.target.name)",
  "スナップショット: \(.snapshotId)",
  "",
  limit_section("対象のリミット"; .limits.target),
  "",
  limit_section("配下のリミット"; .limits.contained),
  "",
  limit_section("後続のリミット"; .limits.downstream),
  "",
  "経路",
  (.tree | tree_lines("  ")),
  "",
  "循環",
  (if (.cycles | length) == 0 then "  なし"
   else (.cycles[] | "  \(.cycleId): " + ([.nodes[].id] | join(", ")) +
     (if .containsImplicitRelation then "  [暗黙的な依存関係を含む]" else "" end) +
     (if .containsUncertainRelation then "  [未確認の依存関係を含む]" else "" end)) end),
  ""
' "$input"
