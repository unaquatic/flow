# Flow

## Environment Variables

- Provider API keys — the variables the model catalog names for each provider, such as `<PROVIDER>_API_KEY`
- flow's own variables are declared and listed in `config/env.go` (`config.EnvVars`), which `flow --help` shows.

## Testing

```sh
go generate ./internal/catalog         # write the catalog snapshot flow compiles in (git ignores it); run before release builds
go test ./... # all tests
go test ./terminal/ui/ -v    # chat UI tests
go test ./extension/snapshot/...     # workspace snapshot extension tests
go test ./terminal/ui/ -run Frames -update   # regenerate UI golden frames
go run github.com/unaquatic/fluid/cmd/fluid design html -o /tmp/goldens.html .   # draw UI golden frames in color
```

- Terminal UI changes add or update a golden frames test (`testutil.AssertGoldenFrames`, `terminal/ui/frames_test.go`) and commit the reviewed `testdata/*.frames.golden` diff.
- UI previews in chat show two frames of the same area, one for text and one for backgrounds, with a legend mapping each symbol to its hex color and name.
- Tests, test data and doc examples use placeholder names (`provider-a`, `lab-a/model-a`, `gw`, `local`, `model-x`, `*.example` hosts), never real providers, labs, models or their ports; take format IDs from `gateway.Format*` constants instead of writing them out.

### Go 1.27+ style — IMPORTANT

This module targets **`go 1.27`** (`go.mod:3`) and the toolchain is Go 1.27.0. **Write modern Go. Do not write Go that looks like it was written in 2018.**

Required:

- **Generics over `interface{}`.** Type parameters, generic type aliases (1.24). `any`, never `interface{}`.
- **Iterators (1.23).** Return `iter.Seq[T]` / `iter.Seq2[K, V]` for sequences that are lazy, large, or computed on demand, and range over functions instead of taking visitor callbacks. Returning a slice is fine when callers index it or the data is already a slice; say in the doc whether it is a copy or shares storage.
- **`slices` / `maps` (1.21+).** `slices.Sort`, `slices.Contains`, `slices.Clone`, `maps.Keys`, etc. Never hand-roll these loops.
- **Builtins**: `min`, `max`, `clear` (1.21). No manual ternary-style helpers.
- **`for i := range n` (1.22)** instead of `for i := 0; i < n; i++`.
- **`errors.AsType[T](err)` (1.26)** instead of declaring a var and calling `errors.As`. Use `errors.Join` for multi-error, `%w` for wrapping.
- **`sync.WaitGroup.Go(fn)` (1.25)** instead of `wg.Add(1)` + `go func(){ defer wg.Done() }()`.
- **`testing/synctest`** for concurrency tests, `b.Loop()` (1.24) for benchmarks, `t.ArtifactDir()` (1.26) for test output files.
- **`math/rand/v2`** (never `math/rand`), `log/slog` (never `log` for structured output), `os.Root` (1.24) for sandboxed FS access.
- Loop variables are per-iteration since 1.22 — **do not** write `x := x` shadow copies.
- **Promoted fields in composite literals (1.27).** Set an embedded struct's fields directly — `Store{q: db, codec: c, db: db}`, not `Store{Objects: Objects{q: db, codec: c}, db: db}`.

Do **not** use `encoding/json/v2` — it is still `GOEXPERIMENT`-gated in 1.26 and will not build.


### Export by default

**Declare structs and methods exported (capitalised) by default.** Write `type Buffer struct` and `func (b *Buffer) Render()`, not `type buffer struct` / `func (b *buffer) render()`.

- When it is a genuinely local implementation detail that has been requested to stay private.
- This applies to struct types, struct fields, methods, and constructors.
- Naming still follows stdlib rules above: exported does not mean stuttering (`buffer.Buffer` is fine, `buffer.NewBuffer` is not) and every exported identifier needs a doc comment starting with its name.
