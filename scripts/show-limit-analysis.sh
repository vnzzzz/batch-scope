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
    "  \(.rank). \(.node.id)  \(.node.name)  \(.fact.sourceText // .fact.duration // .fact.kind)" +
    (if .dependencyDistance != null then "  距離=\(.dependencyDistance)" else "" end) +
    "  確実性=\(.fact.certainty)";

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
  "順位付け規則: \(.policyVersion)",
  "",
  "対象範囲のリミット",
  (if (.limits.scope | length) == 0 then "  なし" else (.limits.scope[] | limit_label) end),
  "",
  "後続の終了時刻リミット",
  (if (.limits.finishByGroups | length) == 0 then "  なし"
   else (.limits.finishByGroups[] |
     "  タイムゾーン: \(.timeZone)  候補=\(.total)" ,
     (if (.items | length) == 0 then "    なし" else (.items[] | limit_label) end)) end),
  "",
  "後続の経過時間リミット",
  (if (.limits.maxElapsed.items | length) == 0 then "  なし" else (.limits.maxElapsed.items[] | limit_label) end),
  "",
  "経路",
  (.tree | tree_lines("  ")),
  "",
  "循環",
  (if (.cycles | length) == 0 then "  なし"
   else (.cycles[] | "  \(.cycleId): " + ([.path[].id] | join(" -> ")) +
     (if .containsUncertainRelation then "  [未確認の依存関係を含む]" else "" end)) end),
  "",
  "処理結果: " + (if .analysisComplete then "指定範囲を最後まで確認" else "未確認範囲あり" end) +
    (if .truncated then "、結果を省略" else "" end)
' "$input"
