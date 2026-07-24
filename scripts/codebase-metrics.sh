#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

production_files=()
while IFS= read -r file; do
  if grep -Eq '^// Code generated .* DO NOT EDIT\.$' "$file"; then
    continue
  fi
  production_files+=("$file")
done < <(find cmd internal -type f -name '*.go' ! -name '*_test.go' -print | LC_ALL=C sort)

test_files=()
while IFS= read -r file; do
  if grep -Eq '^// Code generated .* DO NOT EDIT\.$' "$file"; then
    continue
  fi
  test_files+=("$file")
done < <(find cmd internal -type f -name '*_test.go' -print | LC_ALL=C sort)

line_count() {
  local total=0
  local file
  for file in "$@"; do
    total=$((total + $(wc -l < "$file")))
  done
  printf '%s' "$total"
}

go_cache="${GOCACHE:-${TMPDIR:-/tmp}/securebrain-codebase-metrics-cache}"
module_path="$(GOCACHE="$go_cache" go list -m -f '{{.Path}}')"
imports="$(
  GOCACHE="$go_cache" go list \
    -f '{{.ImportPath}}{{range .Imports}}{{printf "\t%s" .}}{{end}}' ./... |
    awk -F '\t' -v module="$module_path" '
      {
        from = $1
        for (i = 2; i <= NF; i++) {
          if ($i == module || index($i, module "/") == 1) {
            print from "\t" $i
          }
        }
      }
    ' |
    LC_ALL=C sort -u
)"

map_sites="$(
  grep -EnH 'map\[string\](any|interface\{\})' "${production_files[@]}" |
    LC_ALL=C sort -t: -k1,1 -k2,2n || true
)"
discarded_sites="$(
  grep -EnH '(^|[[:space:],])_[[:space:]]*:?=' "${production_files[@]}" |
    LC_ALL=C sort -t: -k1,1 -k2,2n || true
)"
adapter_edges="$(
  printf '%s\n' "$imports" |
    awk -F '\t' -v module="$module_path" '
      $1 == module "/internal/httpapi" &&
      ($2 == module "/internal/store" ||
       $2 == module "/internal/storage" ||
       $2 == module "/internal/openai")
    ' |
    LC_ALL=C sort
)"
oversized_functions="$(
  awk '
    function brace_delta(value, opens, closes) {
      opens = gsub(/\{/, "{", value)
      closes = gsub(/\}/, "}", value)
      return opens - closes
    }
    /^func / {
      active = 1
      start = FNR
      signature = $0
      sub(/[[:space:]]*\{.*/, "", signature)
      depth = brace_delta($0)
      next
    }
    active {
      depth += brace_delta($0)
      if (depth == 0) {
        span = FNR - start + 1
        if (span > 100) {
          print FILENAME ":" start "\t" span "\t" signature
        }
        active = 0
      }
    }
  ' "${production_files[@]}" |
    LC_ALL=C sort -t: -k1,1 -k2,2n
)"

count_nonempty_lines() {
  awk 'NF { count++ } END { print count + 0 }'
}

printf 'METRIC\tVALUE\tDEFINITION\n'
printf 'production_go_lines\t%s\tLines in non-test Go files under cmd/ and internal/\n' \
  "$(line_count "${production_files[@]}")"
printf 'test_go_lines\t%s\tLines in *_test.go files under cmd/ and internal/\n' \
  "$(line_count "${test_files[@]}")"
printf 'untyped_map_sites\t%s\tProduction source lines containing map[string]any or map[string]interface{}\n' \
  "$(printf '%s\n' "$map_sites" | count_nonempty_lines)"
printf 'discarded_result_sites\t%s\tProduction source lines whose assignment ends in a blank identifier\n' \
  "$(printf '%s\n' "$discarded_sites" | count_nonempty_lines)"
printf 'direct_http_adapter_import_edges\t%s\tHTTP package imports of store, storage, or OpenAI adapters\n' \
  "$(printf '%s\n' "$adapter_edges" | count_nonempty_lines)"
printf 'oversized_functions\t%s\tProduction functions longer than 100 physical lines\n' \
  "$(printf '%s\n' "$oversized_functions" | count_nonempty_lines)"

printf '\nPACKAGE_IMPORT_EDGES\n'
printf '%s\n' "$imports"
printf '\nUNTYPED_MAP_SITES\n'
printf '%s\n' "$map_sites"
printf '\nDISCARDED_RESULT_SITES\n'
printf '%s\n' "$discarded_sites"
printf '\nDIRECT_HTTP_ADAPTER_IMPORT_EDGES\n'
printf '%s\n' "$adapter_edges"
printf '\nOVERSIZED_FUNCTIONS\n'
printf '%s\n' "$oversized_functions"
