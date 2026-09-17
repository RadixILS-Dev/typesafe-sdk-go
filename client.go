package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Client is safe for concurrent use. Do not mutate its Models field. Configure
// settings at construction or use per-request options for individual calls.
type Client struct {
	config clientConfig
	// Models provides access to the model-listing endpoint.
	Models *ModelsService
}

// NewClient creates a client. It reads TYPESAFE_API_KEY, TYPESAFE_BASE_URL, and
// TYPESAFE_DEFAULT_MODEL, ignoring empty or whitespace-only environment values.
// No network requests are made until an API method is called.
func NewClient(options ...ClientOption) (*Client, error) {
	config := clientConfig{
		apiKey: env(APIKeyEnv, ""), baseURL: env(BaseURLEnv, DefaultBaseURL),
		model: env(DefaultModelEnv, DefaultModel), timeout: DefaultTimeout,
		headers: make(http.Header), retry: DefaultRetryPolicy(),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("typesafe: nil client option")
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	if config.apiKey == "" {
		return nil, fmt.Errorf("typesafe: no API key provided; use WithAPIKey or set %s", APIKeyEnv)
	}
	config.baseURL = strings.TrimRight(config.baseURL, "/")
	u, err := url.Parse(config.baseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("typesafe: base URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	if config.httpClient == nil {
		config.httpClient = &http.Client{}
	} else if !config.timeoutSet && config.httpClient.Timeout > 0 {
		config.timeout = config.httpClient.Timeout
	}
	// Never follow redirects: match Python and avoid forwarding credentials or
	// replaying a POST at a different endpoint. Leave the caller's client intact.
	httpClient := *config.httpClient
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	config.httpClient = &httpClient
	headers := make(http.Header)
	mergeHeaders(headers, config.headers)
	config.headers = headers
	client := &Client{config: config}
	client.Models = &ModelsService{client: client}
	return client, nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (c *Client) requestConfig(options []RequestOption) (requestConfig, error) {
	config := requestConfig{model: c.config.model, timeout: c.config.timeout,
		headers: c.config.headers.Clone(), retry: c.config.retry.clone()}
	for _, option := range options {
		if option == nil {
			return config, fmt.Errorf("typesafe: nil request option")
		}
		if err := option(&config); err != nil {
			return config, err
		}
	}
	return config, nil
}

// SystemOne evaluates named questions against text, a JSON object, or an array.
// State and question descriptions must be JSON-serializable. A call's context
// bounds both network attempts and retry waits. Questions are encoded once, so
// each retry sends the same body.
func (c *Client) SystemOne(ctx context.Context, state any, questions Questions, options ...RequestOption) (*SystemOneResponse, error) {
	if err := validateQuestions(questions); err != nil {
		return nil, err
	}
	config, err := c.requestConfig(options)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"state": state, "model": config.model, "questions": questions}
	for key, value := range config.extraBody {
		body[key] = value
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("typesafe: request body could not be encoded as JSON: %w", err)
	}
	var result SystemOneResponse
	metadata, err := c.do(ctx, http.MethodPost, "/v1/systemone", data, config,
		func(body []byte) error { return json.Unmarshal(body, &result) })
	if err != nil {
		return nil, err
	}
	result.ResponseMetadata = metadata
	return &result, nil
}

// ModelsService lists models available to the account.
type ModelsService struct{ client *Client }

func (m *ModelsService) List(ctx context.Context, options ...RequestOption) (*ListModelsResponse, error) {
	config, err := m.client.requestConfig(options)
	if err != nil {
		return nil, err
	}
	var result ListModelsResponse
	metadata, err := m.client.do(ctx, http.MethodGet, "/v1/models", nil, config,
		func(body []byte) error { return json.Unmarshal(body, &result) })
	if err != nil {
		return nil, err
	}
	result.ResponseMetadata = metadata
	return &result, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, config requestConfig, decode func([]byte) error) (ResponseMetadata, error) {
	if ctx == nil {
		return ResponseMetadata{}, fmt.Errorf("typesafe: context must not be nil")
	}
	started := time.Now()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return ResponseMetadata{}, err
		}
		metadata, err := c.attempt(ctx, method, path, body, config, attempt, decode)
		if ctx.Err() != nil {
			return ResponseMetadata{}, ctx.Err()
		}
		if err == nil {
			return metadata, nil
		}
		if attempt >= config.retry.MaxRetries || !config.retry.retryable(err) {
			return ResponseMetadata{}, err
		}
		delay := config.retry.retryDelay(attempt, err)
		if config.retry.Timeout > 0 {
			remaining := config.retry.Timeout - time.Since(started)
			if remaining <= 0 || delay >= remaining {
				return ResponseMetadata{}, err
			}
		}
		if c.config.logger != nil {
			c.config.logger.InfoContext(ctx, "typesafe retry", "method", method, "path", path, "retry", attempt+1, "delay", delay)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ResponseMetadata{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) attempt(ctx context.Context, method, path string, body []byte, config requestConfig, attempt int, decode func([]byte) error) (ResponseMetadata, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, config.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(attemptCtx, method, c.config.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return ResponseMetadata{}, fmt.Errorf("typesafe: create request: %w", err)
	}
	request.Header = config.headers.Clone()
	request.Header.Del("X-TypeSafe-Retry-Count")
	request.Header.Set("Authorization", "Bearer "+c.config.apiKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "typesafe-sdk-go/"+Version)
	request.Header.Set("X-TypeSafe-SDK", "typesafe-sdk-go/"+Version)
	request.Header.Set("X-TypeSafe-Runtime", fmt.Sprintf("%s (%s; %s)", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if attempt > 0 {
		request.Header.Set("X-TypeSafe-Retry-Count", strconv.Itoa(attempt))
	}
	response, err := c.config.httpClient.Do(request)
	if err != nil {
		return ResponseMetadata{}, transportError(err, config.timeout)
	}
	data, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		return ResponseMetadata{}, transportError(readErr, config.timeout)
	}
	if c.config.logger != nil {
		c.config.logger.InfoContext(ctx, "typesafe response", "method", method, "path", path,
			"status", response.StatusCode, "request_id", response.Header.Get("X-TypeSafe-Request-ID"))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ResponseMetadata{}, newAPIError(response, data)
	}
	if err := decode(data); err != nil {
		var field *fieldError
		path := ""
		if errors.As(err, &field) {
			path = field.path
		}
		return ResponseMetadata{}, responseValidationError(response, data, path)
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return ResponseMetadata{RequestID: response.Header.Get("X-TypeSafe-Request-ID"), RawHTTPResponse: response, RawBody: data}, nil
}

func transportError(err error, timeout time.Duration) error {
	connection := &ConnectionError{Err: err}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return &TimeoutError{ConnectionError: connection, Timeout: timeout}
	}
	return connection
}
