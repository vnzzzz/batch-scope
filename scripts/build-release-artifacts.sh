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

shopt -s nullglob
schema_files=(schema/*.schema.json)
if [[ ${#schema_files[@]} -eq 0 ]]; then
  echo "公開アーカイブへ同梱するJSON Schemaがありません。" >&2
  exit 1
fi

rm -rf "$output_dir"
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

stage_public_files() {
  local stage="$1"

  cp LICENSE "$stage/"

  # checkoutのREADMEはmain上の最新文書を案内し、配布版だけは対応するタグの文書へ固定する。
  sed \
    "s#https://github.com/vnzzzz/batch-scope/blob/main/#https://github.com/vnzzzz/batch-scope/blob/v${version}/#g" \
    README.md > "$stage/README.md"

  mkdir -p "$stage/skills/public"
  cp -R skills/public/batchscope "$stage/skills/public/"

  rm -rf "$stage/skills/public/batchscope/references/schema"
  mkdir -p "$stage/skills/public/batchscope/references/schema"
  # schema/はGoの埋込コードも持つため、配布対象を命名規則に合うJSON Schemaへ限定する。
  cp "${schema_files[@]}" "$stage/skills/public/batchscope/references/schema/"
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
