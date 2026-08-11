#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/check-release-artifacts.sh <version> [output-dir]

作成済みのRelease archive、Public Skill、JSON Schema、READMEリンク、チェックサムを検査します。
USAGE
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  usage >&2
  exit 2
fi

version="$1"
output_dir="${2:-dist}"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "versionは0.1.0または0.1.0-rc.1の形式で指定してください: $version" >&2
  exit 2
fi

for command in tar sha256sum find diff grep mktemp; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "必要なコマンドが見つかりません: $command" >&2
    exit 1
  }
done

if [[ ! -d skills/public/batchscope || ! -d schema ]]; then
  echo "リポジトリのルートで実行してください。" >&2
  exit 1
fi
if [[ ! -f "$output_dir/checksums.txt" ]]; then
  echo "先にRelease成果物を作成してください: $output_dir/checksums.txt" >&2
  exit 1
fi

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
expected_skill="$work_dir/expected-batchscope"
cp -R skills/public/batchscope "$expected_skill"
rm -rf "$expected_skill/references/schema"

schema_count=0
while IFS= read -r -d '' schema_file; do
  relative="${schema_file#schema/}"
  destination="$expected_skill/references/schema/$relative"
  mkdir -p "$(dirname "$destination")"
  cp "$schema_file" "$destination"
  schema_count=$((schema_count + 1))
done < <(find schema -type f -name '*.schema.json' -print0)
if [[ $schema_count -eq 0 ]]; then
  echo "検査対象のJSON Schemaがありません。" >&2
  exit 1
fi

targets=(linux_amd64 linux_arm64 darwin_amd64 darwin_arm64)
first_files=""

for target in "${targets[@]}"; do
  name="batchscope_${version}_${target}"
  archive="$output_dir/${name}.tar.gz"
  extract_dir="$work_dir/$target"
  root="$extract_dir/$name"

  [[ -f "$archive" ]] || { echo "Release archiveがありません: $archive" >&2; exit 1; }
  mkdir -p "$extract_dir"
  tar -xzf "$archive" -C "$extract_dir"

  for required in batchscope README.md LICENSE skills/public/batchscope/SKILL.md; do
    [[ -f "$root/$required" ]] || { echo "$name: $required がありません。" >&2; exit 1; }
  done

  if find "$root/skills/internal" -type f -print -quit 2>/dev/null | grep -q .; then
    echo "$name: Internal Skillが含まれています。" >&2
    exit 1
  fi

  if ! diff -r "$expected_skill" "$root/skills/public/batchscope" >/dev/null; then
    echo "$name: Public Skillまたは配布JSON Schemaがsourceと一致しません。" >&2
    diff -r "$expected_skill" "$root/skills/public/batchscope" >&2 || true
    exit 1
  fi

  if grep -Eq '\]\((docs/|CONTRIBUTING\.md)' "$root/README.md"; then
    echo "$name: READMEにarchive外を指す相対リンクが残っています。" >&2
    exit 1
  fi
  if grep -q '/blob/main/' "$root/README.md"; then
    echo "$name: READMEにmain固定リンクが残っています。" >&2
    exit 1
  fi
  if ! grep -q "/blob/v${version}/" "$root/README.md"; then
    echo "$name: READMEのrepository内リンクがv${version}へ固定されていません。" >&2
    exit 1
  fi

  files="$work_dir/${target}.files"
  find "$root" -type f -print | sed "s#^$root/##" | sort > "$files"
  if [[ -z "$first_files" ]]; then
    first_files="$files"
  elif ! diff -u "$first_files" "$files" >/dev/null; then
    echo "$name: 公開ファイル構成が他ターゲットと一致しません。" >&2
    diff -u "$first_files" "$files" >&2 || true
    exit 1
  fi
done

(
  cd "$output_dir"
  sha256sum -c checksums.txt
)

printf 'Release artifacts are valid for version %s.\n' "$version"
