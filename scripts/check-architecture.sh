#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

active_manifest="misc/architecture-exceptions.tsv"
baseline_ceiling="misc/architecture-exception-ceiling.tsv"
go_cache="${GOCACHE:-${TMPDIR:-/tmp}/securebrain-architecture-cache}"
module_path="$(GOCACHE="$go_cache" go list -m -f '{{.Path}}')"

temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/securebrain-architecture.XXXXXX")"
trap 'rm -rf "$temporary_dir"' EXIT

imports_file="$temporary_dir/imports.tsv"
violations_file="$temporary_dir/violations.tsv"
active_keys_file="$temporary_dir/active-keys.tsv"
ceiling_keys_file="$temporary_dir/ceiling-keys.tsv"

GOCACHE="$go_cache" go list \
  -f '{{.ImportPath}}{{range .Imports}}{{printf "\t%s" .}}{{end}}' ./... |
  awk -F '\t' -v module="$module_path" '
    {
      from = $1
      for (i = 2; i <= NF; i++) {
        print from "\t" $i
      }
    }
  ' |
  LC_ALL=C sort -u > "$imports_file"

awk -F '\t' -v module="$module_path" '
  function under(path, root) {
    return path == root || index(path, root "/") == 1
  }
  function transport_capability(path, prefix, rest, slash) {
    prefix = module "/internal/httpapi/"
    if (index(path, prefix) != 1) {
      return ""
    }
    rest = substr(path, length(prefix) + 1)
    slash = index(rest, "/")
    return slash ? substr(rest, 1, slash - 1) : rest
  }
  function transport_shared(capability) {
    return capability == "shared" || capability == "primitives"
  }
  function application(path) {
    return under(path, module "/internal/application") ||
           under(path, module "/internal/app") ||
           under(path, module "/internal/services") ||
           under(path, module "/internal/usecase") ||
           under(path, module "/internal/usecases")
  }
  function adapter(path) {
    return under(path, module "/internal/store") ||
           under(path, module "/internal/storage") ||
           under(path, module "/internal/openai") ||
           under(path, module "/internal/postgres") ||
           under(path, module "/internal/objectstore") ||
           under(path, module "/internal/chatprovider")
  }
  function violation(rule, from, to) {
    print rule "\t" from "\t" to
  }
  {
    from = $1
    to = $2

    if (under(from, module "/internal/httpapi") && adapter(to)) {
      violation("transport_concrete_adapter", from, to)
    } else if (adapter(to) &&
               from != module "/cmd/server" &&
               from != to) {
      violation("concrete_adapter_outside_composition", from, to)
    }

    if (under(from, module "/internal/httpapi") &&
        (to == "github.com/jackc/pgx/v5" ||
         under(to, "github.com/jackc/pgx/v5"))) {
      violation("transport_infrastructure_protocol", from, to)
    }

    from_capability = transport_capability(from)
    to_capability = transport_capability(to)
    if (from_capability != "" && to_capability != "" &&
        from_capability != to_capability &&
        !transport_shared(to_capability)) {
      violation("sideways_transport_import", from, to)
    }

    if (application(from) &&
        (under(to, module "/internal/httpapi") ||
         adapter(to) ||
         under(to, module "/internal/config") ||
         to == "net/http" ||
         to == "database/sql" ||
         under(to, "github.com/jackc/pgx/v5"))) {
      violation("application_outer_layer_import", from, to)
    }

    if (under(from, module "/internal/domain") &&
        (application(to) ||
         under(to, module "/internal/httpapi") ||
         adapter(to) ||
         under(to, module "/internal/config") ||
         to == "net/http" ||
         to == "database/sql" ||
         under(to, "github.com/jackc/pgx/v5"))) {
      violation("domain_outer_layer_import", from, to)
    }

    if (adapter(from) &&
        (under(to, module "/internal/httpapi") ||
         under(to, module "/internal/config") ||
         under(to, module "/cmd"))) {
      violation("adapter_outer_layer_import", from, to)
    }
  }
' "$imports_file" | LC_ALL=C sort -u > "$violations_file"

validate_manifest() {
  local manifest="$1"
  awk -F '\t' '
    BEGIN { valid = 1 }
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    NF != 5 {
      printf "%s:%d: expected five tab-separated fields, got %d\n", FILENAME, FNR, NF > "/dev/stderr"
      valid = 0
      next
    }
    $1 !~ /^[a-z][a-z0-9_]*$/ {
      printf "%s:%d: invalid rule name %s\n", FILENAME, FNR, $1 > "/dev/stderr"
      valid = 0
    }
    $4 !~ /^Task [0-9]+(\.[a-z])?$/ {
      printf "%s:%d: invalid owning task %s\n", FILENAME, FNR, $4 > "/dev/stderr"
      valid = 0
    }
    length($5) < 20 {
      printf "%s:%d: removal condition is not specific\n", FILENAME, FNR > "/dev/stderr"
      valid = 0
    }
    {
      key = $1 "\t" $2 "\t" $3
      if (seen[key]++) {
        printf "%s:%d: duplicate exception %s\n", FILENAME, FNR, key > "/dev/stderr"
        valid = 0
      }
    }
    END { exit valid ? 0 : 1 }
  ' "$manifest"
}

validate_manifest "$active_manifest"
validate_manifest "$baseline_ceiling"

awk -F '\t' '!/^[[:space:]]*#/ && NF { print $1 "\t" $2 "\t" $3 }' \
  "$active_manifest" | LC_ALL=C sort -u > "$active_keys_file"
awk -F '\t' '!/^[[:space:]]*#/ && NF { print $1 "\t" $2 "\t" $3 }' \
  "$baseline_ceiling" | LC_ALL=C sort -u > "$ceiling_keys_file"

broadened="$(comm -23 "$active_keys_file" "$ceiling_keys_file")"
unapproved="$(comm -23 "$violations_file" "$active_keys_file")"
stale="$(comm -13 "$violations_file" "$active_keys_file")"

failed=0
if [[ -n "$broadened" ]]; then
  printf 'architecture exceptions exceed the Task 1.d baseline ceiling:\n%s\n' "$broadened" >&2
  failed=1
fi
if [[ -n "$unapproved" ]]; then
  printf 'unapproved production dependency violations:\n%s\n' "$unapproved" >&2
  failed=1
fi
if [[ -n "$stale" ]]; then
  printf 'obsolete architecture exceptions must be removed:\n%s\n' "$stale" >&2
  failed=1
fi
if ((failed)); then
  exit 1
fi

printf 'architecture check passed (%s active exceptions)\n' \
  "$(awk 'END { print NR + 0 }' "$active_keys_file")"
