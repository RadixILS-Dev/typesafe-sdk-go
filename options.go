package typesafe

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const (
	APIKeyEnv       = "TYPESAFE_API_KEY"
	BaseURLEnv      = "TYPESAFE_BASE_URL"
	DefaultModelEnv = "TYPESAFE_DEFAULT_MODEL"
	DefaultBaseURL  = "https://api.typesafe.ai"
	DefaultModel    = "jev-latest"
	DefaultTimeout  = 10 * time.Second
	Version         = "0.1.0"
)

// ClientOption configures a client. Explicit values take precedence over the environment.
type ClientOption func(*clientConfig) error

// RequestOption overrides configuration for one call without modifying the client.
type RequestOption func(*requestConfig) error

type clientConfig struct {
	apiKey, baseURL, model string
	timeout                time.Duration
	timeoutSet             bool
	headers                http.Header
	retry                  RetryPolicy
	httpClient             *http.Client
	logger                 *slog.Logger
}

type requestConfig struct {
	model     string
	timeout   time.Duration
	headers   http.Header
	retry     RetryPolicy
	extraBody map[string]any
}

func WithAPIKey(key string) ClientOption {
	return func(c *clientConfig) error { c.apiKey = key; return nil }
}
func WithBaseURL(url string) ClientOption {
	return func(c *clientConfig) error { c.baseURL = url; return nil }
}
func WithModel(model string) ClientOption {
	return func(c *clientConfig) error { c.model = model; return nil }
}

// WithTimeout sets a timeout for each complete HTTP attempt. Unlike Python's
// per-operation HTTP timeouts, this includes connecting, headers, and reading
// the body. The caller's context independently bounds the whole SDK call.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *clientConfig) error {
		if timeout <= 0 {
			return fmt.Errorf("typesafe: timeout must be positive")
		}
		c.timeout, c.timeoutSet = timeout, true
		return nil
	}
}

// WithHTTPClient uses a caller-owned HTTP client. The SDK never closes it or
// mutates it. A positive http.Client.Timeout is inherited unless WithTimeout
// is provided; a zero timeout inherits the SDK default. The supplied client's
// own timeout remains an independent upper bound.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *clientConfig) error {
		if client == nil {
			return fmt.Errorf("typesafe: HTTP client must not be nil")
		}
		c.httpClient = client
		return nil
	}
}

// WithHeaders adds default headers. Authentication, SDK identification, Accept,
// and retry-count headers are controlled by the SDK, regardless of overrides.
func WithHeaders(headers http.Header) ClientOption {
	return func(c *clientConfig) error { c.headers = headers.Clone(); return nil }
}

// WithRetryPolicy replaces the entire retry policy. Start with
// DefaultRetryPolicy when changing only individual settings.
func WithRetryPolicy(policy RetryPolicy) ClientOption {
	return func(c *clientConfig) error {
		if err := policy.validate(); err != nil {
			return err
		}
		c.retry = policy.clone()
		return nil
	}
}

// WithLogger enables request metadata and retry logging. Request/response bodies
// and headers are never logged, since they may contain sensitive data.
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *clientConfig) error { c.logger = logger; return nil }
}

func WithRequestModel(model string) RequestOption {
	return func(c *requestConfig) error { c.model = model; return nil }
}
func WithRequestTimeout(timeout time.Duration) RequestOption {
	return func(c *requestConfig) error {
		if timeout <= 0 {
			return fmt.Errorf("typesafe: timeout must be positive")
		}
		c.timeout = timeout
		return nil
	}
}
func WithRequestHeaders(headers http.Header) RequestOption {
	return func(c *requestConfig) error { mergeHeaders(c.headers, headers); return nil }
}
func WithRequestRetryPolicy(policy RetryPolicy) RequestOption {
	return func(c *requestConfig) error {
		if err := policy.validate(); err != nil {
			return err
		}
		c.retry = policy.clone()
		return nil
	}
}

// WithExtraBody shallowly merges fields over the SystemOne body. Existing
// state, model, and questions can be replaced. Values are not deep-merged.
// It has no effect on Models.List.
func WithExtraBody(body map[string]any) RequestOption {
	return func(c *requestConfig) error {
		c.extraBody = make(map[string]any, len(body))
		for k, v := range body {
			c.extraBody[k] = v
		}
		return nil
	}
}

func mergeHeaders(dst, src http.Header) {
	for key, values := range src {
		dst[http.CanonicalHeaderKey(key)] = append([]string(nil), values...)
	}
}
