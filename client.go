package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// Client can be reused by concurrent goroutines. Configuration is fixed at construction.
type Client struct{ config clientConfig }

// SystemOne evaluates named questions against JSON-serializable state.
// Question validation belongs to the API. The context bounds the entire call,
// including retry waits; each attempt sends the same encoded body.
func (c *Client) SystemOne(ctx context.Context, state any, questions Questions) (*SystemOneResponse, error) {
	body, err := json.Marshal(struct {
		State     any       `json:"state"`
		Model     string    `json:"model"`
		Questions Questions `json:"questions"`
	}{state, c.config.Model, questions})
	if err != nil {
		return nil, fmt.Errorf("typesafe: encode request: %w", err)
	}
	data, headers, err := c.request(ctx, http.MethodPost, "/v1/systemone", body)
	if err != nil {
		return nil, err
	}
	result, err := decodeSystemOne(data)
	if err != nil {
		return nil, withRequestID(err, headers.Get("X-TypeSafe-Request-ID"))
	}
	result.RequestID = headers.Get("X-TypeSafe-Request-ID")
	return result, nil
}

// ListModels lists the models available to the account.
func (c *Client) ListModels(ctx context.Context) (*ListModelsResponse, error) {
	data, headers, err := c.request(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, err
	}
	result, err := decodeModels(data)
	if err != nil {
		return nil, withRequestID(err, headers.Get("X-TypeSafe-Request-ID"))
	}
	result.RequestID = headers.Get("X-TypeSafe-Request-ID")
	return result, nil
}

func (c *Client) request(ctx context.Context, method, path string, body []byte) ([]byte, http.Header, error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("typesafe: context must not be nil")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.config.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("typesafe: create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "typesafe-sdk-go/"+Version)
	request.Header.Set("X-TypeSafe-SDK", "typesafe-sdk-go/"+Version)
	request.Header.Set("X-TypeSafe-Runtime", fmt.Sprintf("%s (%s; %s)", runtime.Version(), runtime.GOOS, runtime.GOARCH))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	started := time.Now()
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// A fresh request avoids reusing a consumed body or state retained by a transport.
		next := request.Clone(ctx)
		if request.GetBody != nil {
			next.Body, err = request.GetBody()
			if err != nil {
				return nil, nil, err
			}
		}
		if attempt > 1 {
			next.Header.Set("X-TypeSafe-Retry-Count", strconv.Itoa(attempt-1))
		}
		data, headers, err := c.send(next)
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if err == nil {
			return data, headers, nil
		}
		if attempt >= c.config.MaxAttempts || !retryable(err) {
			return nil, nil, err
		}
		delay := retryDelay(attempt-1, headers, time.Now())
		if delay >= 30*time.Second-time.Since(started) {
			return nil, nil, err
		}
		if err := waitForRetry(ctx, delay); err != nil {
			return nil, nil, err
		}
	}
}

// send owns and closes the network response body. Decode errors are handled
// outside the retry loop so malformed successful responses are never retried.
func (c *Client) send(request *http.Request) ([]byte, http.Header, error) {
	response, err := c.config.HTTPClient.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.Header, fmt.Errorf("typesafe: read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, response.Header, newAPIError(response, data)
	}
	return data, response.Header, nil
}
