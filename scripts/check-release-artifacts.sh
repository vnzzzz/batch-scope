#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/check-release-artifacts.sh <version> [output-dir]

作成済みのRelease archive、Public Skill、JSON Schema、デモスナップショット、READMEリンク、チェックサムを検査します。
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

for command in tar sha256sum find diff grep mktemp awk; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "必要なコマンドが見つかりません: $command" >&2
    exit 1
  }
done

if [[ ! -d skills/public/batchscope || ! -d schema || ! -d examples/demo/snapshot ]]; then
  echo "リポジトリのルートで実行してください。" >&2
  exit 1
fi
if [[ ! -f "$output_dir/checksums.txt" ]]; then
  echo "先にRelease成果物を作成してください: $output_dir/checksums.txt" >&2
  exit 1
fi
if [[ ! -f "$output_dir/batchscope_demo_snapshot.tar.gz" ]]; then
  echo "デモスナップショットassetがありません: $output_dir/batchscope_demo_snapshot.tar.gz" >&2
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

demo_extract="$work_dir/demo"
mkdir -p "$demo_extract"
tar -xzf "$output_dir/batchscope_demo_snapshot.tar.gz" -C "$demo_extract"
for demo_file in manifest.json nodes.ndjson relations.ndjson; do
  [[ -f "$demo_extract/$demo_file" ]] || {
    echo "デモスナップショットassetに必要なファイルがありません: $demo_file" >&2
    exit 1
  }
  if ! diff -u "examples/demo/snapshot/$demo_file" "$demo_extract/$demo_file" >/dev/null; then
    echo "デモスナップショットassetがsourceと一致しません: $demo_file" >&2
    diff -u "examples/demo/snapshot/$demo_file" "$demo_extract/$demo_file" >&2 || true
    exit 1
  fi
done

demo_files="$work_dir/demo.files"
find "$demo_extract" -type f -print | sed "s#^$demo_extract/##" | sort > "$demo_files"
printf '%s\n' manifest.json nodes.ndjson relations.ndjson | sort > "$work_dir/expected-demo.files"
if ! diff -u "$work_dir/expected-demo.files" "$demo_files" >/dev/null; then
  echo "デモスナップショットassetのファイル構成が想定と一致しません。" >&2
  diff -u "$work_dir/expected-demo.files" "$demo_files" >&2 || true
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

  if [[ -e "$root/skills/internal" ]]; then
    echo "$name: Internal Skillが含まれています。" >&2
    exit 1
  fi

  if ! diff -r "$expected_skill" "$root/skills/public/batchscope" >/dev/null; then
    echo "$name: Public Skillまたは配布JSON Schemaがsourceと一致しません。" >&2
    diff -r "$expected_skill" "$root/skills/public/batchscope" >&2 || true
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

  while IFS= read -r markdown_link; do
    target_path="${markdown_link#](}"
    target_path="${target_path%)}"
    target_path="${target_path%%#*}"
    if [[ -z "$target_path" || ! -e "$root/$target_path" ]]; then
      echo "$name: READMEの相対リンク先がarchive内にありません: $markdown_link" >&2
      exit 1
    fi
  done < <(grep -Eo '\]\(([^#/:][^):]*)\)' "$root/README.md" || true)

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

expected_checksum_files="$work_dir/expected-checksum.files"
actual_checksum_files="$work_dir/actual-checksum.files"
printf '%s\n' \
  "batchscope_${version}_linux_amd64.tar.gz" \
  "batchscope_${version}_linux_arm64.tar.gz" \
  "batchscope_${version}_darwin_amd64.tar.gz" \
  "batchscope_${version}_darwin_arm64.tar.gz" \
  'batchscope_demo_snapshot.tar.gz' \
  | sort > "$expected_checksum_files"
awk '{print $2}' "$output_dir/checksums.txt" | sed 's#^\*##' | sort > "$actual_checksum_files"
if ! diff -u "$expected_checksum_files" "$actual_checksum_files" >/dev/null; then
  echo "checksums.txtの対象ファイル集合がRelease成果物と一致しません。" >&2
  diff -u "$expected_checksum_files" "$actual_checksum_files" >&2 || true
  exit 1
fi

(
  cd "$output_dir"
  sha256sum -c checksums.txt
)

printf 'Release artifacts are valid for version %s.\n' "$version"
