# Contributing

## Development

Go 1.26 or later (the minimum supported by `go.mod`; CI tests that version and
the latest stable release).

```sh
go build ./...          # library and examples
go test -race ./...     # all tests, including the example candidate finders
go vet ./...
gofmt -l .              # must print nothing
staticcheck ./...       # go install honnef.co/go/tools/cmd/staticcheck@latest
```

Tests never call the live API. They use `httptest` servers and injected
`http.RoundTripper`s, so they are deterministic and safe to run offline. Only
the programs under `examples/` make real requests, and only when you run them
with `TYPESAFE_API_KEY` set.

## Conventions

- Keep `Client` safe for concurrent use and configured entirely at
  construction; do not add setters that mutate a shared client.
- Every call takes a `context.Context` that bounds the whole operation,
  including retry waits. Never retry a caller cancellation or deadline.
- Never turn a missing or wrong-typed response field into a Go zero value.
  Return a `*ResponseError` naming the JSON field path, and keep unknown answer
  types visible in `RawAnswers`.
- Preserve native transport and context errors so `errors.Is`/`errors.As` keep
  working, and keep credentials, query strings, and bodies out of error text.
- Retry only idempotent-by-construction requests; `SystemOne` replays the exact
  encoded body, so new endpoints must tolerate replay before opting in.
- Update `CHANGELOG.md` in the same pull request as a user-visible change.

## Examples

Each example is a runnable `main` package under `examples/` that documents one
usage pattern, states in a comment whether it needs an API key, and is listed in
the README. Put logic that can be tested offline (candidate finders, output
formatting) in functions and cover it with a `main_test.go` that makes no
network calls.

## Releasing

1. Update `Version` in `config.go` and add a `CHANGELOG.md` section for it.
2. Run the full development checks above.
3. Tag and push the release:

   ```sh
   VERSION="$(sed -n 's/^[[:space:]]*Version[[:space:]]*=[[:space:]]*"\(.*\)".*/\1/p' config.go | head -1)"
   git tag -a "v$VERSION" -m "v$VERSION"
   git push origin main --follow-tags
   ```

   CI rejects a tag whose name does not match `Version`, so the User-Agent and
   the published module version cannot drift apart.
4. Confirm it resolves as a dependency:

   ```sh
   go get github.com/RadixILS-Dev/typesafe-sdk-go@v0.1.0
   ```
