package typesafe

import "net/http"

// ClientOption configures a client at construction.
type ClientOption func(*clientConfig)

// WithAPIKey overrides TYPESAFE_API_KEY.
func WithAPIKey(key string) ClientOption {
	return func(c *clientConfig) { c.APIKey = key }
}

// WithBaseURL overrides TYPESAFE_BASE_URL and the default API URL.
func WithBaseURL(url string) ClientOption {
	return func(c *clientConfig) { c.BaseURL = url }
}

// WithModel overrides TYPESAFE_DEFAULT_MODEL and the default model.
func WithModel(model string) ClientOption {
	return func(c *clientConfig) { c.Model = model }
}

// WithMaxAttempts sets the total number of attempts, including the initial
// request. The default is three; one disables retries. Must be positive.
func WithMaxAttempts(attempts int) ClientOption {
	return func(c *clientConfig) { c.MaxAttempts = attempts }
}

// WithHTTPClient uses the supplied non-nil client unchanged, including its
// timeout and redirect policy. The SDK does not mutate or close it.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *clientConfig) { c.HTTPClient = client }
}
