#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-api.sh [options]

公開APIへデモスナップショットを取り込み、主要な応答をE2Eで確認します。
接続先を指定しない場合は、一時バイナリと一時データディレクトリでサービスを起動します。
起動済みサービスを指定する場合は、スナップショット未取込のサービスを使用してください。

出力は既定で人間向けに要約します。
後続リミット解析は、チャットツール等からJOB IDを指定して問い合わせた場合の返答を想定した形で表示します。
APIの加工前レスポンスを確認したい場合は --output raw を指定してください。

Options:
  --base-url URL       起動済みサービスを検査するURL。指定時はビルドと起動を行わない
  --port PORT          ローカル起動で使うポート。未指定時は空いているポートを選ぶ
  --timeout SEC        起動と取込の待機上限（既定値: 30秒）
  --output MODE        出力形式。human（既定）またはraw
  -h, --help           このヘルプを表示する

Environment:
  BATCHSCOPE_SMOKE_BASE_URL  --base-urlの既定値
  BATCHSCOPE_SMOKE_PORT      --portの既定値
  BATCHSCOPE_SMOKE_TIMEOUT   --timeoutの既定値
  BATCHSCOPE_SMOKE_OUTPUT    --outputの既定値
USAGE
}

fail() {
  echo "エラー: $*" >&2
  exit 1
}

base_url="${BATCHSCOPE_SMOKE_BASE_URL:-}"
port="${BATCHSCOPE_SMOKE_PORT:-}"
timeout_seconds="${BATCHSCOPE_SMOKE_TIMEOUT:-30}"
output_mode="${BATCHSCOPE_SMOKE_OUTPUT:-human}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base-url)
      [[ $# -ge 2 ]] || fail '--base-urlにはURLが必要です。'
      base_url="$2"
      shift 2
      ;;
    --base-url=*)
      base_url="${1#*=}"
      shift
      ;;
    --port)
      [[ $# -ge 2 ]] || fail '--portにはポート番号が必要です。'
      port="$2"
      shift 2
      ;;
    --port=*)
      port="${1#*=}"
      shift
      ;;
    --timeout)
      [[ $# -ge 2 ]] || fail '--timeoutには秒数が必要です。'
      timeout_seconds="$2"
      shift 2
      ;;
    --timeout=*)
      timeout_seconds="${1#*=}"
      shift
      ;;
    --output)
      [[ $# -ge 2 ]] || fail '--outputにはhumanまたはrawが必要です。'
      output_mode="$2"
      shift 2
      ;;
    --output=*)
      output_mode="${1#*=}"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      fail "未対応の引数です: $1"
      ;;
  esac
done

[[ "$timeout_seconds" =~ ^[1-9][0-9]*$ ]] || fail '--timeoutは1以上の整数で指定してください。'
[[ "$output_mode" == human || "$output_mode" == raw ]] || fail '--outputはhumanまたはrawで指定してください。'
if [[ -n "$base_url" && -n "$port" ]]; then
  fail '--base-urlと--portは同時に指定できません。'
fi
if [[ -n "$base_url" && ! "$base_url" =~ ^https?://[^/?#]+/?$ ]]; then
  fail '--base-urlはhttp://またはhttps://で始まるURLを指定してください。'
fi

for required_command in curl jq tar mktemp; do
  command -v "$required_command" >/dev/null 2>&1 || fail "必要なコマンドが見つかりません: $required_command"
done

[[ -f examples/demo/snapshot/manifest.json ]] || fail 'リポジトリのルートで実行してください。'

work_dir="$(mktemp -d)"
server_pid=''
server_log="$work_dir/server.log"
response_body="$work_dir/response.json"
response_headers="$work_dir/response.headers"
response_status=''

cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [[ -n "$work_dir" && -d "$work_dir" ]]; then
    rm -rf "$work_dir"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

step() {
  local number="$1"
  local title="$2"
  if [[ "$output_mode" == human ]]; then
    printf '\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
    printf '%s/9  %s\n' "$number" "$title"
    printf '━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n'
  else
    printf '\n=== [%s/9] %s ===\n' "$number" "$title"
  fi
}

human_request_result() {
  local method="$1"
  local path="$2"
  printf '%-6s %-52s → HTTP %s\n' "$method" "$path" "$response_status"
}

raw_request() {
  local method="$1"
  local url="$2"
  shift 2

  printf '> %s %s\n' "$method" "$url"
  printf '$ curl -i --request %q' "$method"
  local arg
  for arg in "$@"; do
    printf ' %q' "$arg"
  done
  printf ' %q\n' "$url"
}

raw_response() {
  if [[ -s "$response_headers" ]]; then
    sed 's/\r$//' "$response_headers"
  fi
  if [[ -s "$response_body" ]]; then
    cat "$response_body"
    printf '\n'
  else
    printf '(bodyなし)\n'
  fi
}

show_failure_response() {
  printf '\n--- response ---\n' >&2
  raw_response >&2
}

fail_response() {
  echo "エラー: $*" >&2
  show_failure_response
  exit 1
}

http_request() {
  local method="$1"
  local url="$2"
  local max_time="$3"
  shift 3

  if [[ "$output_mode" == raw ]]; then
    raw_request "$method" "$url" "$@"
  fi

  : >"$response_body"
  : >"$response_headers"
  if ! response_status="$(curl --silent --show-error \
    --connect-timeout "$max_time" \
    --max-time "$max_time" \
    --request "$method" \
    --dump-header "$response_headers" \
    --output "$response_body" \
    --write-out '%{http_code}' \
    "$@" \
    "$url")"; then
    fail "HTTP要求に失敗しました: $method $url"
  fi

  if [[ "$output_mode" == raw ]]; then
    raw_response
  fi
}

header_value() {
  local name="$1"
  awk -v wanted="$name" '
    {
      line = $0
      sub(/\r$/, "", line)
      colon = index(line, ":")
      if (colon == 0) {
        next
      }
      key = substr(line, 1, colon - 1)
      if (tolower(key) == tolower(wanted)) {
        value = substr(line, colon + 1)
        sub(/^[[:space:]]+/, "", value)
      }
    }
    END { print value }
  ' "$response_headers"
}

assert_status() {
  local expected="$1"
  local label="$2"
  if [[ "$response_status" != "$expected" ]]; then
    fail_response "$labelのHTTPステータスが${expected}ではありません: $response_status"
  fi
}

assert_json() {
  local filter="$1"
  local message="$2"
  shift 2
  if ! jq --exit-status "$@" "$filter" "$response_body" >/dev/null; then
    fail_response "$message"
  fi
}

port_is_open() {
  local candidate="$1"
  (exec 3<>"/dev/tcp/127.0.0.1/$candidate") 2>/dev/null
}

choose_free_port() {
  local attempt candidate
  # 空き確認からlistenまでは不可分ではない。競合時は子プロセスの終了として検知できる一時実行用のため、このTOCTOUを許容する。
  for ((attempt = 0; attempt < 200; attempt++)); do
    candidate=$((20000 + ((RANDOM * 32768 + RANDOM + $$ + attempt) % 40000)))
    if ! port_is_open "$candidate"; then
      printf '%s\n' "$candidate"
      return
    fi
  done
  fail '空いているローカルポートを選べませんでした。--portで指定してください。'
}

wait_for_service() {
  local deadline=$((SECONDS + timeout_seconds))
  local status

  while ((SECONDS < deadline)); do
    # listen失敗などの起動エラーを接続タイムアウトまで隠さず、子プロセス終了時点でログとともに返す。
    if [[ -n "$server_pid" ]] && ! kill -0 "$server_pid" 2>/dev/null; then
      echo 'サービスのログ:' >&2
      sed -n '1,80p' "$server_log" >&2
      fail 'サービスが起動完了前に終了しました。'
    fi
    if status="$(curl --silent --output /dev/null --write-out '%{http_code}' \
      --connect-timeout 1 --max-time 1 "$base_url/healthz" 2>/dev/null)" && [[ "$status" == '200' ]]; then
      return
    fi
    sleep 0.2
  done

  if [[ -s "$server_log" ]]; then
    echo 'サービスのログ:' >&2
    sed -n '1,80p' "$server_log" >&2
  fi
  fail "サービスが${timeout_seconds}秒以内に応答しませんでした: $base_url"
}

start_local_service() {
  command -v go >/dev/null 2>&1 || fail 'ローカル起動にはgoが必要です。'
  if [[ -z "$port" ]]; then
    port="$(choose_free_port)"
  elif [[ ! "$port" =~ ^[0-9]{1,5}$ ]] || ((10#$port < 1 || 10#$port > 65535)); then
    fail '--portは1から65535の整数で指定してください。'
  elif port_is_open "$((10#$port))"; then
    fail "指定したポートは使用中です: $port"
  fi
  port="$((10#$port))"
  base_url="http://127.0.0.1:$port"

  printf '[準備] 一時バイナリをビルド\n'
  if ! go build -o "$work_dir/batchscope" ./cmd/batchscope; then
    fail '一時バイナリのビルドに失敗しました。'
  fi
  mkdir -p "$work_dir/data"
  printf '[起動] %s\n' "$base_url"
  "$work_dir/batchscope" serve -listen "127.0.0.1:$port" -data-dir "$work_dir/data" >"$server_log" 2>&1 &
  server_pid=$!
}

prepare_snapshot_archive() {
  snapshot_id="$(jq --exit-status --raw-output '.snapshotId | select(type == "string" and length > 0)' \
    examples/demo/snapshot/manifest.json)" || fail 'デモのmanifest.jsonからsnapshotIdを取得できません。'
  archive="$work_dir/demo-snapshot.tar.gz"
  if ! tar -C examples/demo/snapshot -czf "$archive" manifest.json nodes.ndjson relations.ndjson; then
    fail 'デモスナップショットのtar.gz作成に失敗しました。'
  fi
}

human_json_value() {
  local filter="$1"
  jq --raw-output "$filter" "$response_body"
}

check_health() {
  step 1 'ヘルスチェック'
  http_request GET "$base_url/healthz" "$timeout_seconds"
  assert_status 200 'ヘルスチェック'
  assert_json '.status == "ok"' 'ヘルスチェックのstatusがokではありません。'

  if [[ "$output_mode" == human ]]; then
    human_request_result GET '/healthz'
    printf '  プロセスは正常に応答しています。\n'
  fi
}

check_initial_readiness() {
  step 2 '取込前の準備状態'
  http_request GET "$base_url/readyz" "$timeout_seconds"
  # ローカル起動と--base-url指定のどちらも、スナップショット未取込の空状態から始めることを不変条件とする。
  assert_status 503 '取込前の準備状態'
  assert_json '.status == "not_ready" and .reason == "snapshot_not_loaded"' \
    '取込前の準備状態がnot_ready / snapshot_not_loadedではありません。'

  if [[ "$output_mode" == human ]]; then
    human_request_result GET '/readyz'
    printf '  状態: 未準備（snapshot未取込）\n'
  fi
}

import_snapshot() {
  local import_deadline import_state location import_url remaining retry_after sleep_seconds previous_state

  step 3 'デモスナップショットを送信'
  http_request POST "$base_url/v1/snapshot-imports" "$timeout_seconds" \
    --header 'Content-Type: application/vnd.batchscope.snapshot+gzip' \
    --data-binary "@$archive"
  assert_status 202 'スナップショット取込'

  # 取込IDや状態URLの構成をクライアント側で再現せず、APIが返した追跡先を正本とする。
  location="$(header_value 'Location')"
  [[ -n "$location" ]] || fail_response 'スナップショット取込のLocationヘッダーがありません。'
  [[ "$location" == /* ]] || fail_response "Locationヘッダーが相対パスではありません: $location"

  retry_after="$(header_value 'Retry-After')"
  [[ "$retry_after" =~ ^[1-9][0-9]*$ ]] || fail_response \
    "スナップショット取込のRetry-Afterが1以上の整数ではありません: ${retry_after:-<missing>}"

  import_url="$base_url$location"

  if [[ "$output_mode" == human ]]; then
    human_request_result POST '/v1/snapshot-imports'
    printf '  取込受付: OK\n'
    printf '  追跡先:   %s\n' "$location"
    printf '  再確認:   %s秒後\n' "$retry_after"
  fi

  step 4 '取込完了を待機'
  if [[ "$output_mode" == human ]]; then
    printf 'GET    %-52s\n' "$location"
    printf '  状態: '
  fi

  import_deadline=$((SECONDS + timeout_seconds))
  import_state=''
  previous_state=''

  while ((SECONDS < import_deadline)); do
    remaining=$((import_deadline - SECONDS))
    ((remaining > 0)) || break
    sleep_seconds="$retry_after"
    if ((sleep_seconds > remaining)); then
      sleep_seconds="$remaining"
    fi
    sleep "$sleep_seconds"

    remaining=$((import_deadline - SECONDS))
    ((remaining > 0)) || break
    http_request GET "$import_url" "$remaining"
    if [[ "$response_status" != '200' ]]; then
      fail_response "取込状況のHTTPステータスが200ではありません: $response_status"
    fi
    import_state="$(jq --exit-status --raw-output '.state | select(type == "string")' "$response_body")" \
      || fail_response '取込状況のstateを取得できません。'

    if [[ "$import_state" != "$previous_state" ]]; then
      if [[ "$output_mode" == human ]]; then
        if [[ -n "$previous_state" ]]; then
          printf ' → '
        fi
        printf '%s' "$import_state"
      fi
      previous_state="$import_state"
    fi

    case "$import_state" in
      succeeded)
        assert_json '.snapshotId == $snapshot_id' \
          '取込結果のsnapshotIdがmanifest.jsonと一致しません。' \
          --arg snapshot_id "$snapshot_id"
        if [[ "$output_mode" == human ]]; then
          printf '\n'
          printf '  snapshotId: %s\n' "$snapshot_id"
        fi
        break
        ;;
      failed)
        [[ "$output_mode" == human ]] && printf '\n'
        fail_response 'スナップショットの取込が失敗しました。'
        ;;
      accepted|validating|building|activating)
        ;;
      *)
        [[ "$output_mode" == human ]] && printf '\n'
        fail_response "取込状況に未知のstateが返されました: $import_state"
        ;;
    esac
  done

  [[ "$output_mode" != human || -z "$import_state" || "$import_state" == succeeded ]] || printf '\n'
  [[ "$import_state" == 'succeeded' ]] || fail \
    "スナップショットの取込が${timeout_seconds}秒以内に完了しませんでした。"
}

check_ready_after_import() {
  step 5 '取込後の準備状態'
  http_request GET "$base_url/readyz" "$timeout_seconds"
  assert_status 200 '取込後の準備状態'
  assert_json '.status == "ready" and .reason == "snapshot_loaded"' \
    '取込後の準備状態がready / snapshot_loadedではありません。'

  if [[ "$output_mode" == human ]]; then
    human_request_result GET '/readyz'
    printf '  状態: ready（snapshot loaded）\n'
  fi
}

check_current_snapshot() {
  step 6 '現在のスナップショット'
  http_request GET "$base_url/v1/snapshots/current" "$timeout_seconds"
  assert_status 200 '現在のスナップショット'
  assert_json '.snapshotId == $snapshot_id' \
    '現在のスナップショットIDがmanifest.jsonと一致しません。' \
    --arg snapshot_id "$snapshot_id"

  if [[ "$output_mode" == human ]]; then
    human_request_result GET '/v1/snapshots/current'
    printf '  snapshotId: %s\n' "$(human_json_value '.snapshotId')"
    printf '  nodes: %s / relations: %s / limits: %s\n' \
      "$(human_json_value '.nodeCount')" \
      "$(human_json_value '.relationCount')" \
      "$(human_json_value '.limitCount')"
  fi
}

check_status() {
  step 7 'サービス状態'
  http_request GET "$base_url/v1/status" "$timeout_seconds"
  assert_status 200 'サービス状態'
  assert_json '.state == "ready"' 'サービス状態のstateがreadyではありません。'
  assert_json '.snapshot.snapshotId == $snapshot_id' \
    'サービス状態のsnapshotIdがmanifest.jsonと一致しません。' \
    --arg snapshot_id "$snapshot_id"

  if [[ "$output_mode" == human ]]; then
    human_request_result GET '/v1/status'
    printf '  state: %s\n' "$(human_json_value '.state')"
    printf '  snapshot: %s\n' "$(human_json_value '.snapshot.snapshotId')"
  fi
}

check_target_search() {
  step 8 'JOB-Aを完全一致検索'
  http_request GET "$base_url/v1/targets?query=JOB-A&type=job" "$timeout_seconds"
  assert_status 200 '完全一致検索'
  assert_json '.snapshotId == $snapshot_id' \
    '完全一致検索のsnapshotIdがmanifest.jsonと一致しません。' \
    --arg snapshot_id "$snapshot_id"
  assert_json '.truncated == false' '完全一致検索がtruncated=trueを返しました。'
  assert_json '(.items | length) == 1 and .items[0].id == "JOB-A" and .items[0].type == "job"' \
    '完全一致検索でJOB-Aを一件取得できません。'

  if [[ "$output_mode" == human ]]; then
    human_request_result GET '/v1/targets?query=JOB-A&type=job'
    jq --raw-output '
      .items[0] |
      "  見つかった対象: \(.id)  \(.name)\n" +
      "  種別: \(.type)\n" +
      (if .path then "  path: \(.path)" else "" end)
    ' "$response_body"
  fi
}

show_analysis_human() {
  jq --raw-output '
    def owner:
      .limitOwner as $o |
      ($o.id + (if ($o.name // "") != "" then "  " + $o.name else "" end));

    def fact_text:
      if .fact.kind == "finish_by" then
        (.fact.sourceText // (
          "営業日+" + ((.fact.businessDayOffset // 0) | tostring) +
          " " + (.fact.localTime // "?") +
          " (" + (.fact.timeZone // "?") + ")"
        ))
      elif .fact.kind == "max_elapsed" then
        (.fact.sourceText // .fact.duration // "最大経過時間")
      else
        (.fact.sourceText // "raw limit")
      end;

    def kind_label:
      if .fact.kind == "finish_by" then "終了時刻"
      elif .fact.kind == "max_elapsed" then "最大経過時間"
      else "その他"
      end;

    def item_line:
      "  • " + owner + "  [" + kind_label + "]\n" +
      "      " + fact_text +
      (if .fact.kind == "finish_by" and (.fact.timeZone // "") != "" then
         "\n      timezone: " + .fact.timeZone
       else "" end) +
      (if .scopeRoot then
         "\n      scope: " + .scopeRoot.id +
         (if (.scopeRoot.name // "") != "" then "  " + .scopeRoot.name else "" end)
       else "" end) +
      (if (.alternatePathCount // 0) > 0 then
         "\n      別経路: +" + ((.alternatePathCount // 0) | tostring)
       else "" end);

    def items_in($bucket):
      [
        ($bucket.finishByGroups[]?.items[]?),
        ($bucket.maxElapsed.items[]?),
        ($bucket.raw.items[]?)
      ];

    def bucket_total($bucket):
      ([ $bucket.finishByGroups[]?.total ] + [ $bucket.maxElapsed.total, $bucket.raw.total ] | add);

    def reason_text:
      if . == "cycle_without_limit" then "循環内にリミットなし"
      elif . == "terminal_without_limit" then "終端までリミットなし"
      elif . == "non_traversable_node_type" then "探索対象外ノードで終了"
      else .
      end;

    . as $r |
    (bucket_total(.limits.target)) as $target_total |
    (bucket_total(.limits.contained)) as $contained_total |
    (bucket_total(.limits.downstream)) as $downstream_total |

    "── 想定チャット返答 ─────────────────────────────────────",
    "",
    "入力例: /batchscope limits " + .target.id,
    "",
    "JOB: " + .target.id + "  " + (.target.name // ""),
    (if .target.path then "Path: " + .target.path else empty end),
    "Snapshot: " + .snapshotId,
    "",
    "リミット: 合計 " + (($target_total + $contained_total + $downstream_total) | tostring) + "件" +
      "（対象 " + ($target_total | tostring) +
      " / 配下 " + ($contained_total | tostring) +
      " / 後続 " + ($downstream_total | tostring) + "）",
    "",

    (if $target_total > 0 then
       "【対象自身】",
       (items_in(.limits.target)[] | item_line),
       ""
     else empty end),

    (if $contained_total > 0 then
       "【配下】",
       (items_in(.limits.contained)[] | item_line),
       ""
     else empty end),

    (if $downstream_total > 0 then
       "【後続】",
       (items_in(.limits.downstream)[] | item_line),
       ""
     else empty end),

    "注意:",
    "  循環: " + ((.cycles | length) | tostring) + "件" +
      (if (.cycles | length) > 0 then
         " (" + ([.cycles[].cycleId] | join(", ")) + ")"
       else "" end),
    "  リミット未検出の経路: " + ((.uncoveredRoutes | length) | tostring) + "件",
    (
      .uncoveredRoutes[]? |
      "    - " + .boundary.id +
      (if (.boundary.name // "") != "" then "  " + .boundary.name else "" end) +
      " — " + (.reason | reason_text)
    )
  ' "$response_body"
}

check_downstream_limit_analysis() {
  step 9 'JOB-Aの後続リミット解析'
  http_request GET "$base_url/v1/downstream-limit-analysis?targetId=JOB-A" "$timeout_seconds"
  assert_status 200 '後続リミット解析'
  assert_json '.snapshotId == $snapshot_id' \
    '後続リミット解析のsnapshotIdがmanifest.jsonと一致しません。' \
    --arg snapshot_id "$snapshot_id"
  assert_json '.target.id == "JOB-A"' '後続リミット解析のtarget.idがJOB-Aではありません。'
  assert_json '
    def limit_total:
      ([.finishByGroups[]?.total] + [.maxElapsed.total, .raw.total] | add);
    (.limits.downstream | limit_total) == 5
  ' '後続リミット解析のdownstream limit件数が5ではありません。'
  assert_json '
    any(
      .limits.downstream.finishByGroups[]?.items[],
      .limits.downstream.maxElapsed.items[],
      .limits.downstream.raw.items[];
      .fact.id == "LIMIT-JOB-C-FINISH"
    )
  ' 'LIMIT-JOB-C-FINISHが後続リミットにありません。'
  assert_json '([.cycles[].cycleId] | sort) == ["cycle-1", "cycle-2"]' \
    'cycle一覧がデモデータと一致しません。'
  assert_json '(.uncoveredRoutes | length) == 3' \
    'uncoveredRoutesの件数が3ではありません。'
  assert_json '
    any(.uncoveredRoutes[]; .boundary.id == "JOB-CYCLE-A" and .reason == "cycle_without_limit") and
    any(.uncoveredRoutes[]; .boundary.id == "JOB-TERMINAL" and .reason == "terminal_without_limit") and
    any(.uncoveredRoutes[]; .boundary.id == "UNIT-BOUNDARY" and .reason == "non_traversable_node_type")
  ' 'uncoveredRoutesの代表境界がデモデータと一致しません。'

  if [[ "$output_mode" == human ]]; then
    human_request_result GET '/v1/downstream-limit-analysis?targetId=JOB-A'
    printf '\n'
    show_analysis_human
  fi
}

if [[ -z "$base_url" ]]; then
  start_local_service
else
  base_url="${base_url%/}"
  printf '[接続] 起動済みサービス: %s\n' "$base_url"
fi

wait_for_service
prepare_snapshot_archive
check_health
check_initial_readiness
import_snapshot
check_ready_after_import
check_current_snapshot
check_status
check_target_search
check_downstream_limit_analysis

printf '\n成功: 公開APIのE2Eスモークテストが完了しました（snapshotId=%s）。\n' "$snapshot_id"
