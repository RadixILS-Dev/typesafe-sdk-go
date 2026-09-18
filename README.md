# TypeSafe AI Go SDK

[![CI](https://github.com/RadixILS-Dev/typesafe-sdk-go/actions/workflows/ci.yml/badge.svg)](https://github.com/RadixILS-Dev/typesafe-sdk-go/actions/workflows/ci.yml)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/RadixILS-Dev/typesafe-sdk-go.svg)](https://pkg.go.dev/github.com/RadixILS-Dev/typesafe-sdk-go)

A client for [TypeSafe AI](https://typesafe.ai). The wire
format and default retry behavior follow the
[Python SDK v0.6.0](https://github.com/typesafe-ai/typesafe-sdk-python/tree/420ef4ffb612d5a539a1e0f0fe883ff6770340af),
with a smaller, Go-native API.

## Quick start

Requires Go 1.26 or later. The module declares `go 1.26` as its minimum; CI
tests that version and the latest stable release.

```sh
go get github.com/RadixILS-Dev/typesafe-sdk-go
export TYPESAFE_API_KEY='your-api-key'
```

Import `github.com/RadixILS-Dev/typesafe-sdk-go` as package `typesafe`:

```go
client, err := typesafe.NewClient()
if err != nil {
    log.Fatal(err)
}
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := client.SystemOne(ctx, "I was charged twice. Please help ASAP.", typesafe.Questions{
    "billing": typesafe.Noul{Instructions: "Is this about billing?"},
    "tone": typesafe.Choice{
        Instructions: "What is the tone?",
        Criteria: map[string]string{"calm": "", "angry": ""},
    },
    "urgency": typesafe.Score{
        Instructions: "How urgent is this?",
        Criteria: []string{"low", "medium", "high"},
    },
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Nouls["billing"].Noul, result.Choices["tone"].Choice, result.Scores["urgency"].Score)
```

The complete program is in [`examples/basic/main.go`](examples/basic/main.go).
`go run ./examples/basic` makes a real API request.

## Configuration

`NewClient()` uses environment variables and defaults. For explicit settings:

```go
client, err := typesafe.NewClient(
    typesafe.WithAPIKey("your-api-key"),
    typesafe.WithModel("jev-latest"),
    typesafe.WithMaxAttempts(1), // No retries.
)
```

`WithAPIKey`, `WithBaseURL`, and `WithModel` override `TYPESAFE_API_KEY`,
`TYPESAFE_BASE_URL`, and `TYPESAFE_DEFAULT_MODEL`. Options apply in order; the last
option for a setting wins. Blank environment values are ignored. The defaults
are `https://api.typesafe.ai` and `jev-latest`; an API key is required.
`WithMaxAttempts` accepts a positive total attempt count (default three).

The default HTTP client has a 10-second timeout per attempt and does not follow
redirects. Use `WithHTTPClient` to control timeouts, redirects, transport,
or instrumentation. It is used **unchanged**, not copied, reconfigured, or closed;
a supplied client with a zero timeout has no per-attempt timeout. Contexts bound
the entire call, including retry waits. Clients are safe for concurrent calls;
do not mutate a shared HTTP client or request data during use.

## Questions and results

`Questions` maps names to `Noul`, `Choice`, `Score`, or raw JSON objects. Typed
questions add their own `type` tags. State, instructions, and descriptions can
contain structured JSON. For example, `Choice.Criteria` accepts
`map[string]any{"billing": nil}` and `Score.Criteria` accepts `[]any`.

Nil optional fields are omitted; explicit empty strings remain empty strings,
not JSON null. Raw question objects (`map[string]any`) are sent unchanged.
The API validates question schemas, including empty questions and score rubrics.
Serialization errors are returned before sending.

Results contain `Nouls`, `Choices`, `Scores`, `Model`, `Usage`, and `RequestID`:

- Noul: `Noul` is the probability of yes.
- Choice: `Choice`, `Confidence`, and label-keyed `Probabilities`.
- Score: `Score`, `Confidence`, and integer-keyed `Legend` and `Probabilities`.
- Usage: `*int64` counts distinguish missing values from zero.

`RawAnswers` retains every answer's original JSON, including unknown types and
extra fields. Unknown types do not appear in the typed maps. Use normal Go map
lookup checks when an answer may be absent. Missing or malformed required fields
in known answers return an error instead of silently becoming zero values.

Model listing is a direct call:

```go
models, err := client.ListModels(ctx)
// models.Models contains Name, Description, and ReleaseDate.
```

## Convenience methods

These helpers use `SystemOne`, including the client's model, retries, and context
handling. Both state and instructions accept text or structured JSON.

| Method | Purpose | Result |
| --- | --- | --- |
| `Pick(ctx, state, instructions, candidates)` | Select a supplied span, or `typesafe.NoneChoice` (`"none"`) | `ChoiceAnswer, error` |
| `Classify(ctx, state, instructions, options)` | Select from a fixed label set; no automatic fallback | `ChoiceAnswer, error` |
| `IsTrue(ctx, state, instructions)` | Evaluate a yes/no question | `float64, error` — probability of yes |

```go
document := "Invoice total: USD 42.00"

picked, err := client.Pick(ctx, document, "Which amount is the invoice total?",
    []string{"USD 42.00", "USD 21.00"})
if err != nil {
    log.Fatal(err)
}
fmt.Println(picked.Choice, picked.Confidence)

classified, err := client.Classify(ctx, document, "Which currency is used?",
    []string{"USD", "EUR", "GBP"})
if err != nil {
    log.Fatal(err)
}
fmt.Println(classified.Choice, classified.Confidence)

probability, err := client.IsTrue(ctx, document, "Does this state an invoice total?")
if err != nil {
    log.Fatal(err)
}
fmt.Println(probability)
```

`Pick` does not extract candidate spans; the caller supplies them. Candidate text
is preserved exactly, without trimming or normalization. The label `"none"` is
reserved in `Pick`; passing it as a candidate returns an error. It is allowed as
an ordinary label in `Classify`. Duplicate labels collapse into one option, just
as they do in the Python cookbook's dictionaries. An empty candidate list still
makes a request containing only the no-match option; an empty classification
list is sent to the API for validation.

Choice results also include `Probabilities`. A missing/wrong-type answer or a
selection outside the supplied options returns `ResponseError`, not a silent
zero value. `IsTrue` does not apply a threshold. These are single-question calls;
use `SystemOne` when batching questions or accessing usage and raw responses.

### Candidate extraction example

[`examples/extraction/main.go`](examples/extraction/main.go) finds email addresses
with a regex, deduplicates them in document order, and uses `Pick` to select the
receipt destination and From address independently. Normalization stays in
application code, after selection.

For the sample document, the intended selections are `dana.personal@gmail.com`
for the receipt and `dana.whit@acme-corp.com` for the sender. Actual selections and
confidence values come from the model; the example handles a no-match result.

## Examples

Every example is a runnable program in its own package. All of them make real API
requests and need `TYPESAFE_API_KEY`.

| Example | Demonstrates |
| --- | --- |
| [`examples/basic`](examples/basic/main.go) | One `SystemOne` call with a Noul, Choice, and Score question |
| [`examples/claims`](examples/claims/main.go) | Many Noul questions in one call over a JSON claim file, cross-checked offline |
| [`examples/extraction`](examples/extraction/main.go) | Regex candidates plus `Pick` per role, with no-match handling |
| [`examples/phone`](examples/phone/main.go) | `Pick` and `Classify` over one shared candidate list |

```sh
# Each needs TYPESAFE_API_KEY and makes live calls.
go run ./examples/basic
```

The parts that can be checked offline are plain functions with their own tests:

```sh
go test ./examples/...   # offline helpers only; no API access
```

`examples/claims` shows the pattern for a batch of related yes/no questions: one
`SystemOne` call carries the whole claim file, each answer keeps its own
probability, and the answers that can be computed from the file (dates, amounts,
coverage flags) are recomputed in Go and compared. Any `DISAGREE` line, and any
answer missing from the response, is reported rather than defaulted.

Runnable, offline godoc examples, including `*APIError` handling, live in
[`example_test.go`](example_test.go) and appear on
[pkg.go.dev](https://pkg.go.dev/github.com/RadixILS-Dev/typesafe-sdk-go).

## Retries and errors

The client retries HTTP 408, 429, 5xx, and transport/read failures, up to
the attempt limit set by `WithMaxAttempts`. Backoff starts at 500 ms, doubles up
to 5 seconds, and subtracts
up to 25% jitter. `retry-after-ms` takes precedence over `Retry-After` (seconds or
HTTP date). A fixed 30-second scheduling budget prevents starting a retry whose
delay would reach that budget; it does not interrupt an in-flight request.
Use a context deadline for a hard limit. Caller cancellation is never retried.

- `*APIError` exposes `StatusCode`, `Message`, `Body`, `Headers`, and `RequestID`.
- `*ResponseError` identifies invalid response data by `FieldPath` and `RequestID`.
- Transport and context errors preserve their native causes for `errors.Is` and
  `errors.As`; inspect `net.Error` for transport timeouts.

Errors are returned to the caller to handle or log. The SDK does not install a
logger or log request bodies, credentials, or headers.


## Development

```sh
go build ./...        # library and examples
go test -race ./...
go vet ./...
gofmt -l .            # must print nothing
staticcheck ./...     # optional; CI runs it
```

Tests use local servers and injected transports, not the live TypeSafe API, so
they pass offline and under `-race`. Only the programs under `examples/` make
real requests, and only when you run them.

CI (`.github/workflows/ci.yml`) runs all of the above on Go 1.26 and the latest
stable release, checks that `go.mod` is tidy, and rejects a release tag whose
name does not match the `Version` constant. See
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the release checklist,
[`CHANGELOG.md`](CHANGELOG.md) for released changes, and
[`SECURITY.md`](SECURITY.md) for reporting issues and credential guidance.

