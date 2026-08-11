#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-api.sh [options]

公開APIへデモスナップショットを取り込み、主要な応答をE2Eで確認します。
接続先を指定しない場合は、一時バイナリと一時データディレクトリでサービスを起動します。
起動済みサービスを指定する場合は、スナップショット未取込のサービスを使用してください。

通常は、各APIのRequest、curl相当コマンド、raw response、jq整形結果、検査結果を表示します。

Options:
  --base-url URL    起動済みサービスを検査するURL。指定時はビルドと起動を行わない
  --port PORT       ローカル起動で使うポート。未指定時は空いているポートを選ぶ
  --timeout SEC     起動と取込の待機上限（既定値: 30秒）
  --quiet           Request/responseの詳細を省略し、進捗と成否だけを表示する
  -h, --help        このヘルプを表示する

Environment:
  BATCHSCOPE_SMOKE_BASE_URL  --base-urlの既定値
  BATCHSCOPE_SMOKE_PORT      --portの既定値
  BATCHSCOPE_SMOKE_TIMEOUT   --timeoutの既定値
USAGE
}

fail() {
  echo "エラー: $*" >&2
  exit 1
}

base_url="${BATCHSCOPE_SMOKE_BASE_URL:-}"
port="${BATCHSCOPE_SMOKE_PORT:-}"
timeout_seconds="${BATCHSCOPE_SMOKE_TIMEOUT:-30}"
quiet=false

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
    --quiet)
      quiet=true
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
  printf '\n[%s/9] %s\n' "$number" "$title"
}

pass_check() {
  [[ "$quiet" == true ]] && return
  printf '  ✓ %s\n' "$1"
}

show_request() {
  local method="$1"
  local url="$2"
  shift 2

  [[ "$quiet" == true ]] && return

  printf '\nRequest\n'
  printf '  %s %s\n' "$method" "$url"
  printf '\ncurl\n'
  printf '  curl -i --request %q' "$method"
  local arg
  for arg in "$@"; do
    printf ' %q' "$arg"
  done
  printf ' %q\n' "$url"
}

show_saved_response() {
  local force="${1:-false}"
  if [[ "$quiet" == true && "$force" != true ]]; then
    return
  fi

  printf '\nRaw response\n'
  if [[ -s "$response_headers" ]]; then
    sed 's/\r$//' "$response_headers" | sed 's/^/  /'
  else
    printf '  (response headersなし)\n'
  fi
  if [[ -s "$response_body" ]]; then
    sed 's/^/  /' "$response_body"
  else
    printf '  (bodyなし)\n'
  fi

  if [[ -s "$response_body" ]] && jq empty "$response_body" >/dev/null 2>&1; then
    printf '\nPretty response\n'
    jq . "$response_body" | sed 's/^/  /'
  fi
}

fail_response() {
  echo "エラー: $*" >&2
  show_saved_response true >&2
  exit 1
}

http_request() {
  local trace_mode="$1"
  local method="$2"
  local url="$3"
  local max_time="$4"
  shift 4

  if [[ "$trace_mode" == show ]]; then
    show_request "$method" "$url" "$@"
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

  if [[ "$trace_mode" == show ]]; then
    show_saved_response
  fi
}

header_value() {
  local name="$1"
  awk -v wanted="$name" '
    BEGIN { IGNORECASE = 1 }
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
  pass_check "HTTP $expected"
}

assert_json() {
  local filter="$1"
  local failure_message="$2"
  local success_message="$3"
  shift 3
  if ! jq --exit-status "$@" "$filter" "$response_body" >/dev/null; then
    fail_response "$failure_message"
  fi
  pass_check "$success_message"
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

  echo "[準備] 一時バイナリをビルドします。"
  if ! go build -o "$work_dir/batchscope" ./cmd/batchscope; then
    fail '一時バイナリのビルドに失敗しました。'
  fi
  mkdir -p "$work_dir/data"
  echo "[起動] $base_url でサービスを起動します。"
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

check_health() {
  step 1 'GET /healthz — ヘルスチェック'
  http_request show GET "$base_url/healthz" "$timeout_seconds"
  assert_status 200 'ヘルスチェック'
  assert_json '.status == "ok"' \
    'ヘルスチェックのstatusがokではありません。' \
    'status = "ok"'
}

check_initial_readiness() {
  step 2 'GET /readyz — 取込前の準備状態'
  http_request show GET "$base_url/readyz" "$timeout_seconds"
  # ローカル起動と--base-url指定のどちらも、スナップショット未取込の空状態から始めることを不変条件とする。
  assert_status 503 '取込前の準備状態'
  assert_json '.status == "not_ready" and .reason == "snapshot_not_loaded"' \
    '取込前の準備状態がnot_ready / snapshot_not_loadedではありません。' \
    'status = "not_ready", reason = "snapshot_not_loaded"'
}

import_snapshot() {
  local import_deadline import_state location import_url remaining retry_after sleep_seconds previous_state

  step 3 'POST /v1/snapshot-imports — デモスナップショットを送信'
  http_request show POST "$base_url/v1/snapshot-imports" "$timeout_seconds" \
    --header 'Content-Type: application/vnd.batchscope.snapshot+gzip' \
    --data-binary "@$archive"
  assert_status 202 'スナップショット取込'

  # 取込IDや状態URLの構成をクライアント側で再現せず、APIが返した追跡先を正本とする。
  location="$(header_value 'Location')"
  [[ -n "$location" ]] || fail_response 'スナップショット取込のLocationヘッダーがありません。'
  [[ "$location" == /* ]] || fail_response "Locationヘッダーが相対パスではありません: $location"
  pass_check "Location = $location"

  retry_after="$(header_value 'Retry-After')"
  [[ "$retry_after" =~ ^[1-9][0-9]*$ ]] || fail_response \
    "スナップショット取込のRetry-Afterが1以上の整数ではありません: ${retry_after:-<missing>}"
  pass_check "Retry-After = ${retry_after}s"

  import_url="$base_url$location"
  step 4 'GET /v1/snapshot-imports/{importId} — 取込完了をpoll'

  if [[ "$quiet" != true ]]; then
    printf '\nFollow\n'
    printf '  GET %s\n' "$import_url"
    printf '  Retry-After: %ss\n' "$retry_after"
    printf '\nPoll states\n'
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
    http_request poll GET "$import_url" "$remaining"
    if [[ "$response_status" != '200' ]]; then
      fail_response "取込状況のHTTPステータスが200ではありません: $response_status"
    fi
    import_state="$(jq --exit-status --raw-output '.state | select(type == "string")' "$response_body")" \
      || fail_response '取込状況のstateを取得できません。'

    if [[ "$import_state" != "$previous_state" && "$quiet" != true ]]; then
      printf '  %s\n' "$import_state"
      previous_state="$import_state"
    fi

    case "$import_state" in
      succeeded)
        if [[ "$quiet" != true ]]; then
          printf '\nFinal status response\n'
          show_request GET "$import_url"
          show_saved_response
        fi
        pass_check 'HTTP 200'
        assert_json '.snapshotId == $snapshot_id' \
          '取込結果のsnapshotIdがmanifest.jsonと一致しません。' \
          "snapshotId = \"$snapshot_id\"" \
          --arg snapshot_id "$snapshot_id"
        break
        ;;
      failed)
        fail_response 'スナップショットの取込が失敗しました。'
        ;;
      accepted|validating|building|activating)
        ;;
      *)
        fail_response "取込状況に未知のstateが返されました: $import_state"
        ;;
    esac
  done

  [[ "$import_state" == 'succeeded' ]] || fail \
    "スナップショットの取込が${timeout_seconds}秒以内に完了しませんでした。"
}

check_ready_after_import() {
  step 5 'GET /readyz — 取込後の準備状態'
  http_request show GET "$base_url/readyz" "$timeout_seconds"
  assert_status 200 '取込後の準備状態'
  assert_json '.status == "ready" and .reason == "snapshot_loaded"' \
    '取込後の準備状態がready / snapshot_loadedではありません。' \
    'status = "ready", reason = "snapshot_loaded"'
}

check_current_snapshot() {
  step 6 'GET /v1/snapshots/current — 現在のスナップショット'
  http_request show GET "$base_url/v1/snapshots/current" "$timeout_seconds"
  assert_status 200 '現在のスナップショット'
  assert_json '.snapshotId == $snapshot_id' \
    '現在のスナップショットIDがmanifest.jsonと一致しません。' \
    "snapshotId = \"$snapshot_id\"" \
    --arg snapshot_id "$snapshot_id"
}

check_status() {
  step 7 'GET /v1/status — サービス状態'
  http_request show GET "$base_url/v1/status" "$timeout_seconds"
  assert_status 200 'サービス状態'
  assert_json '.state == "ready"' \
    'サービス状態のstateがreadyではありません。' \
    'state = "ready"'
  assert_json '.snapshot.snapshotId == $snapshot_id' \
    'サービス状態のsnapshotIdがmanifest.jsonと一致しません。' \
    "snapshot.snapshotId = \"$snapshot_id\"" \
    --arg snapshot_id "$snapshot_id"
}

# 以下の期待値はデモスナップショットとレスポンス例に由来する。公開DTO全体の厳密一致はGoテストへ委ね、ここではE2Eで代表値が失われていないことだけを検知する。
check_target_search() {
  step 8 'GET /v1/targets?query=JOB-A&type=job — 完全一致検索'
  http_request show GET "$base_url/v1/targets?query=JOB-A&type=job" "$timeout_seconds"
  assert_status 200 '完全一致検索'
  assert_json '.snapshotId == $snapshot_id' \
    '完全一致検索のsnapshotIdがmanifest.jsonと一致しません。' \
    "snapshotId = \"$snapshot_id\"" \
    --arg snapshot_id "$snapshot_id"
  assert_json '.truncated == false' \
    '完全一致検索がtruncated=trueを返しました。' \
    'truncated = false'
  assert_json '(.items | length) == 1 and .items[0].id == "JOB-A" and .items[0].type == "job"' \
    '完全一致検索でJOB-Aを一件取得できません。' \
    'items[0] = JOB-A (job)'
}

check_downstream_limit_analysis() {
  step 9 'GET /v1/downstream-limit-analysis?targetId=JOB-A — 後続リミット解析'
  http_request show GET "$base_url/v1/downstream-limit-analysis?targetId=JOB-A" "$timeout_seconds"
  assert_status 200 '後続リミット解析'
  assert_json '.snapshotId == $snapshot_id' \
    '後続リミット解析のsnapshotIdがmanifest.jsonと一致しません。' \
    "snapshotId = \"$snapshot_id\"" \
    --arg snapshot_id "$snapshot_id"
  assert_json '.target.id == "JOB-A"' \
    '後続リミット解析のtarget.idがJOB-Aではありません。' \
    'target.id = "JOB-A"'
  assert_json '
    def limit_total:
      ([.finishByGroups[]?.total] + [.maxElapsed.total, .raw.total] | add);
    (.limits.downstream | limit_total) == 5
  ' '後続リミット解析のdownstream limit件数が5ではありません。' \
    'downstream limits = 5'
  assert_json '
    any(
      .limits.downstream.finishByGroups[]?.items[],
      .limits.downstream.maxElapsed.items[],
      .limits.downstream.raw.items[];
      .fact.id == "LIMIT-JOB-C-FINISH"
    )
  ' 'LIMIT-JOB-C-FINISHが後続リミットにありません。' \
    'LIMIT-JOB-C-FINISHを検出'
  assert_json '([.cycles[].cycleId] | sort) == ["cycle-1", "cycle-2"]' \
    'cycle一覧がデモデータと一致しません。' \
    'cycles = cycle-1, cycle-2'
  assert_json '(.uncoveredRoutes | length) == 3' \
    'uncoveredRoutesの件数が3ではありません。' \
    'uncoveredRoutes = 3'
  assert_json '
    any(.uncoveredRoutes[]; .boundary.id == "JOB-CYCLE-A" and .reason == "cycle_without_limit") and
    any(.uncoveredRoutes[]; .boundary.id == "JOB-TERMINAL" and .reason == "terminal_without_limit") and
    any(.uncoveredRoutes[]; .boundary.id == "UNIT-BOUNDARY" and .reason == "non_traversable_node_type")
  ' 'uncoveredRoutesの代表境界がデモデータと一致しません。' \
    'cycle / terminal / non-traversable の代表境界を検出'
}

if [[ -z "$base_url" ]]; then
  start_local_service
else
  base_url="${base_url%/}"
  echo "[接続] 起動済みサービスを検査します: $base_url"
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
