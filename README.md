# TypeSafe AI Go SDK

A Go client for [TypeSafe AI](https://typesafe.ai), following the behavior of the
[Python SDK v0.6.0](https://github.com/typesafe-ai/typesafe-sdk-python/tree/420ef4ffb612d5a539a1e0f0fe883ff6770340af).
Uses only the Go standard library.

## Quick start

```sh
go get github.com/rocktavious/typesafe-sdk-go
export TYPESAFE_API_KEY='your-api-key'
```

The import path is `github.com/rocktavious/typesafe-sdk-go`; the package name is
`typesafe`.

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

See [`examples/basic/main.go`](examples/basic/main.go) for the complete program.
Run it with `go run ./examples/basic` (makes a real API request).

## Configuration

Explicit options override environment variables, which override defaults.
Whitespace-only environment values are ignored.

| Option | Environment variable | Default |
| --- | --- | --- |
| `WithAPIKey` | `TYPESAFE_API_KEY` | Required |
| `WithBaseURL` | `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` |
| `WithModel` | `TYPESAFE_DEFAULT_MODEL` | `jev-latest` |
| `WithTimeout` | — | 10 seconds per HTTP attempt |

Additional options: `WithHTTPClient`, `WithHeaders`, `WithRetryPolicy`, and
`WithLogger` (a `*slog.Logger`). Clients can be reused concurrently. Supplied HTTP
clients are not mutated or closed. Redirects are not followed.

Per-call options are `WithRequestModel`, `WithRequestTimeout`,
`WithRequestHeaders`, `WithRequestRetryPolicy`, and `WithExtraBody`. They do not
modify the client's configuration. Extra body fields shallowly overwrite the
System One body, including `state`, `model`, or `questions` if specified.
Authentication and SDK identification headers cannot be overridden.

## Questions and answers

- **Noul:** returns a yes probability (`Noul`, from 0 to 1).
- **Choice:** returns `Choice`, `Confidence`, and label-keyed `Probabilities`.
- **Score:** returns a probability-weighted `Score`, `Confidence`, and
  integer-keyed `Legend` and `Probabilities`. Levels start at zero.

Question type tags are automatically serialized. State and instructions can be
strings, JSON objects, or arrays. Criteria fields use `any` to support both simple
Go containers and Python-compatible structured descriptions:

```go
question := typesafe.Choice{
    Instructions: "Which team should handle this?",
    Criteria: map[string]any{
        "billing": map[string]any{"examples": []string{"charged twice", "refund"}},
        "other": nil, // JSON null: no description
    },
}
```

`Noul.Criteria` accepts `typesafe.NoulCriteria{"true": "Yes description",
"false": "No description"}`. Structured score rubrics can use `[]any`.
Empty strings remain empty strings; they are **not** rewritten as `null`.
Nil optional fields are omitted; explicit empty strings, maps, and slices are
preserved. A `RawQuestion` can express explicit nulls, extra fields, or future
question types:

```go
questions := typesafe.Questions{
    "custom": typesafe.RawQuestion{"type": "noul", "instructions": nil, "future_option": true},
}
```

Empty question maps and empty score rubrics are rejected before sending. Like
Python, the SDK otherwise leaves detailed question validation to the API.

Responses provide `Answers` plus grouped `Nouls`, `Choices`, and `Scores`. Groups
share the same answer pointers. Unknown response fields are tolerated and unknown
answer types are skipped; their original JSON remains in `RawBody`.

`Model`, `Usage`, `RequestID`, and `RawHTTPResponse` expose response metadata.
Token counts are `*int64`: nil means unreported, distinct from zero. Missing
request IDs are empty strings. The original network body is closed before the
call returns; `RawHTTPResponse.Body` is a buffered in-memory copy. Treat response
maps and answers as read-only when sharing them between goroutines.

## Models

```go
models, err := client.Models.List(ctx)
// models.Models contains Name, Description, and ReleaseDate for each model.
```

## Retries and cancellation

Defaults match Python: two retries after the initial attempt, retrying 408, 429,
all 5xx statuses, connection failures, and per-attempt timeouts. Backoff starts at
500 ms, doubles up to 5 seconds, and subtracts up to 25% jitter. `retry-after-ms`
takes precedence over `Retry-After` (seconds or HTTP date).

```go
policy := typesafe.DefaultRetryPolicy()
policy.MaxRetries = 3
client, err := typesafe.NewClient(typesafe.WithRetryPolicy(policy))

// Disable retries for one call:
result, err := client.SystemOne(ctx, state, questions,
    typesafe.WithRequestRetryPolicy(typesafe.RetryPolicy{}))
```

Policies replace the whole configuration; the zero-value policy disables retries.
The default 30-second retry budget stops scheduling retries whose delays would
reach or exceed the budget. It does not interrupt an in-flight attempt. Use a
context deadline for a hard limit across attempts and waits. Caller cancellation
is never retried. `RetryPolicy.Predicate` can add custom retry conditions.

## Errors

Use `errors.As` to inspect errors. Specific HTTP errors wrap `*typesafe.APIError`,
which contains `StatusCode`, `Message`, `Body`, `Headers`, `Endpoint`, and
`RequestID`:

```go
var apiErr *typesafe.APIError
if errors.As(err, &apiErr) {
    log.Printf("status=%d request_id=%s", apiErr.StatusCode, apiErr.RequestID)
}
```

Specific types include `BadRequestError`, `AuthenticationError`,
`PermissionDeniedError`, `NotFoundError`, `UnprocessableEntityError`,
`RateLimitError`, and `InternalServerError`. `ResponseValidationError` identifies
malformed successful responses using `FieldPath`. Transport failures use
`ConnectionError` or `TimeoutError`, preserving the underlying cause for
`errors.Is` / `errors.As`. Caller cancellation preserves `context.Canceled` or
`context.DeadlineExceeded`.

## Deliberate Go adaptations

- One concurrency-safe client instead of separate sync/async clients.
- Contexts and functional options instead of keyword arguments.
- The 10-second timeout bounds a complete HTTP attempt, including reading its
  body, rather than Python's individual HTTP operations. A supplied HTTP client's
  positive timeout is inherited unless overridden; its own timeout still applies.
- Explicit `WithLogger` rather than configuring global logging through an
  environment variable. Logs contain metadata only, never headers or bodies.
- Caller-owned HTTP resources are not closed; there is no required `Close` call.
- Public responses are ordinary Go structs, not immutable Python objects.

## Development

```sh
go test -race ./...
go vet ./...
```

Tests use local HTTP servers and injected transports; no API key or network
access to TypeSafe is needed. The example is compiled by `go test ./...`, not run.
