# Task 1.d codebase metrics baseline

Captured from commit `f690131` before production responsibilities moved. Run
`./scripts/codebase-metrics.sh` from anywhere in the repository to reproduce the
inventory. The script uses physical source lines and reports production and test
Go separately; tooling and generated files are not included.

| Metric | Baseline | Definition |
| --- | ---: | --- |
| Production Go lines | 6,602 | Non-test `.go` files under `cmd/` and `internal/` |
| Test Go lines | 1,626 | `_test.go` files under `cmd/` and `internal/` |
| Untyped map sites | 66 | Production lines containing `map[string]any` or `map[string]interface{}` |
| Discarded-result sites | 46 | Production assignment lines ending in a blank identifier |
| Direct HTTP-to-adapter import edges | 3 | Imports from `internal/httpapi` to store, storage, or OpenAI |
| Oversized functions | 6 | Production functions longer than 100 physical lines |

The untyped-map and discarded-result figures are candidate inventories rather
than CI limits. The script prints every matching `file:line` occurrence so sites
can be classified and reduced without treating intentionally open metadata or
documented best-effort work as defects.

## Production package import edges

```text
secure-brain/cmd/server -> secure-brain/internal/config
secure-brain/cmd/server -> secure-brain/internal/httpapi
secure-brain/cmd/server -> secure-brain/internal/openai
secure-brain/cmd/server -> secure-brain/internal/storage
secure-brain/cmd/server -> secure-brain/internal/store
secure-brain/internal/httpapi -> secure-brain/internal/assets
secure-brain/internal/httpapi -> secure-brain/internal/domain
secure-brain/internal/httpapi -> secure-brain/internal/openai
secure-brain/internal/httpapi -> secure-brain/internal/query
secure-brain/internal/httpapi -> secure-brain/internal/routes
secure-brain/internal/httpapi -> secure-brain/internal/storage
secure-brain/internal/httpapi -> secure-brain/internal/store
secure-brain/internal/openai -> secure-brain/internal/domain
secure-brain/internal/query -> secure-brain/internal/domain
secure-brain/internal/routes -> secure-brain/internal/domain
secure-brain/internal/storage -> secure-brain/internal/domain
secure-brain/internal/store -> secure-brain/internal/domain
```

The three direct adapter imports are the `internal/httpapi` edges to
`internal/openai`, `internal/storage`, and `internal/store`. Their exact,
task-owned exceptions are in `architecture-exceptions.tsv`.

## Oversized functions

```text
internal/httpapi/handlers_assets.go:52       159  uploadAsset
internal/httpapi/handlers_execution.go:116   152  executeRoute
internal/httpapi/handlers_transfers.go:114   108  acceptTransfer
internal/query/service.go:336                114  queryCSVAsset
internal/routes/policy.go:72                 135  ValidateConfiguration
internal/store/query_paths.go:151            117  LoadQueryPathConfig
```

Line count is deliberately not a per-commit gate. Capture and review this report
at coherent task boundaries.
