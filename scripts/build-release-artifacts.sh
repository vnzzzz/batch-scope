#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/build-release-artifacts.sh <version> <commit> [output-dir]

GitHub Releasesへ登録するOS別アーカイブとチェックサムを作成します。
実行環境はLinuxのDev ContainerまたはGitHub Actionsです。
USAGE
}

if [[ $# -lt 2 || $# -gt 3 ]]; then
  usage >&2
  exit 2
fi

version="$1"
commit="$2"
output_dir="${3:-dist}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "versionは0.1.0または0.1.0-rc.1の形式で指定してください: $version" >&2
  exit 2
fi

for command in go tar sha256sum; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "必要なコマンドが見つかりません: $command" >&2
    exit 1
  }
done

if [[ ! -f README.md ]]; then
  echo "README.mdが見つかりません。リポジトリのルートで実行してください。" >&2
  exit 1
fi

if [[ ! -f LICENSE ]]; then
  echo "公開バイナリへ同梱するLICENSEがありません。ライセンスを決定してから実行してください。" >&2
  exit 1
fi

if [[ ! -d skills/public/batchscope/references ]]; then
  echo "公開アーカイブへ同梱するPublic Skillがありません。" >&2
  exit 1
fi

if [[ ! -d examples/demo/snapshot ]]; then
  echo "公開アーカイブへ同梱するデモスナップショットがありません。" >&2
  exit 1
fi

for demo_file in manifest.json nodes.ndjson relations.ndjson; do
  if [[ ! -f "examples/demo/snapshot/$demo_file" ]]; then
    echo "デモスナップショットに必要なファイルがありません: examples/demo/snapshot/$demo_file" >&2
    exit 1
  fi
done

mapfile -d '' schema_files < <(find schema -type f -name '*.schema.json' -print0)
if [[ ${#schema_files[@]} -eq 0 ]]; then
  echo "公開アーカイブへ同梱するJSON Schemaがありません。" >&2
  exit 1
fi

rm -rf "$output_dir"
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

render_release_readme() {
  local destination="$1"

  # checkoutでは相対リンクを維持し、配布版だけ対応するタグの文書へ固定する。
  sed -E \
    -e "s#\]\((docs/[^)]*)\)#](https://github.com/vnzzzz/batch-scope/blob/v${version}/\1)#g" \
    -e "s#\]\((CONTRIBUTING\\.md[^)]*)\)#](https://github.com/vnzzzz/batch-scope/blob/v${version}/\1)#g" \
    README.md > "$destination"
}

stage_public_files() {
  local stage="$1"
  local schema_file relative destination

  cp LICENSE "$stage/"
  render_release_readme "$stage/README.md"

  mkdir -p "$stage/skills/public"
  cp -R skills/public/batchscope "$stage/skills/public/"

  rm -rf "$stage/skills/public/batchscope/references/schema"
  for schema_file in "${schema_files[@]}"; do
    relative="${schema_file#schema/}"
    destination="$stage/skills/public/batchscope/references/schema/$relative"
    mkdir -p "$(dirname "$destination")"
    cp "$schema_file" "$destination"
  done

  mkdir -p "$stage/examples/demo"
  cp -R examples/demo/snapshot "$stage/examples/demo/"
}

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

for target in "${targets[@]}"; do
  read -r goos goarch <<<"$target"
  name="batchscope_${version}_${goos}_${goarch}"
  stage="$work_dir/$name"
  binary="batchscope"

  mkdir -p "$stage"

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${version} -X main.commit=${commit}" \
      -o "$stage/$binary" \
      ./cmd/batchscope

  stage_public_files "$stage"

  tar -C "$work_dir" -czf "$output_dir/${name}.tar.gz" "$name"
done

(
  cd "$output_dir"
  while IFS= read -r file; do
    sha256sum "$file"
  done < <(find . -maxdepth 1 -type f ! -name checksums.txt -print | sed 's#^\./##' | sort)
) > "$output_dir/checksums.txt"

printf 'Created release artifacts in %s\n' "$output_dir"
