#!/usr/bin/env bash
set -Eeuo pipefail

input="${1:-/dev/stdin}"

if ! command -v jq >/dev/null 2>&1; then
  echo 'jqが必要です。Dev Container内で実行してください。' >&2
  exit 1
fi

jq -r '
  def node_label:
    "[\(.namespace // "default")] \(.localId // .id)  \(.name)";

  def limit_label:
    "    " + (.limitOwner | node_label) + "  \(.fact.sourceText // .fact.duration // .fact.kind)" +
    (if .scopeRoot != null then "  スコープルート=" + (.scopeRoot | node_label) else "" end) +
    "  確実性=\(.fact.certainty)  経路=\(.treeNodeId)" +
    (if .alternatePathCount > 0 then "  代替経路=\(.alternatePathCount)" else "" end);

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

  def connection_label:
    "\(.fromId) -> \(.toId): " +
    (if .viaScope then "scope"
     else ([.viaRelations[] | relation_label] | join(", ")) end);

  def via_label:
    if ((.hiddenConnections // []) | length) > 0 then
      "  <- [圧縮区間: " + ([.hiddenConnections[] | connection_label] | join("; ")) + "]"
    elif (.viaScope // false) then "  <- scope"
    elif ((.viaRelations // []) | length) > 0 then
      "  <- " + ([.viaRelations[] | relation_label] | join(", "))
    else ""
    end;

  def reference_label:
    if .referenceType == "cycle" then "  [循環参照 -> \(.referenceTo), \(.cycleId)]"
    elif .referenceType == "shared" then "  [合流参照 -> \(.referenceTo)]"
    else ""
    end;

  def hidden_label:
    if ((.hiddenConnections // []) | length) == 0 then ""
    else "  [ジョブ\(.hiddenJobCount // 0)件省略: " +
      ((.hiddenNodeIds // []) | join(", ")) +
      (if .hiddenNodeIdsTruncated then ", 以降省略" else "" end) + "]"
    end;

  def tree_lines($indent):
    ($indent + "\(.treeNodeId)  " + (.node | node_label) + via_label + reference_label + hidden_label),
    ((.children // [])[] | tree_lines($indent + "  "));

  def cycle_step:
    "    \(.fromId) -> \(.toId): " +
    (if .viaScope then "scope"
     else ([.viaRelations[] | relation_label] | join(", ")) end);

  "対象: " + (.target | node_label),
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
  "リミット未通過経路",
  (if (.uncoveredRoutes | length) == 0 then "  なし"
   else (.uncoveredRoutes[] |
     "  \(.reason): 境界=" + (.boundary | node_label) + "  経路=\(.treeNodeId)" +
     (if .cycleId != null then "  循環=\(.cycleId)" else "" end)) end),
  "",
  "循環",
  (if (.cycles | length) == 0 then "  なし"
   else (.cycles[] | "  \(.cycleId): " + ([.nodes[] | node_label] | join(", ")) +
     (if .containsImplicitRelation then "  [暗黙的な依存関係を含む]" else "" end) +
     (if .containsUncertainRelation then "  [未確認の依存関係を含む]" else "" end),
     "  一周分の表示経路",
     (.route[] | cycle_step)) end),
  ""
' "$input"
