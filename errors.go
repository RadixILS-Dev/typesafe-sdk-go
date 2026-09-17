package typesafe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// APIError describes an unsuccessful HTTP response. Use errors.As to inspect
// this base type, including when the error has a more specific classification.
type APIError struct {
	StatusCode int
	Body       any
	Headers    http.Header
	RequestID  string
	Endpoint   string
	Message    string
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("%d %s", e.StatusCode, e.Message)
	if e.Endpoint != "" {
		message = e.Endpoint + ": " + message
	}
	if e.RequestID != "" {
		message += " (request_id=" + e.RequestID + ")"
	}
	return message
}

type BadRequestError struct{ *APIError }
type AuthenticationError struct{ *APIError }
type PermissionDeniedError struct{ *APIError }
type NotFoundError struct{ *APIError }
type UnprocessableEntityError struct{ *APIError }
type InternalServerError struct{ *APIError }

// RateLimitError includes the requested delay, when a valid retry header exists.
type RateLimitError struct {
	*APIError
	RetryAfter    time.Duration
	HasRetryAfter bool
}

// ResponseValidationError is a successful HTTP response with missing or
// malformed required data. FieldPath locates the invalid field.
type ResponseValidationError struct {
	*APIError
	FieldPath string
}

func (e *BadRequestError) Unwrap() error          { return e.APIError }
func (e *AuthenticationError) Unwrap() error      { return e.APIError }
func (e *PermissionDeniedError) Unwrap() error    { return e.APIError }
func (e *NotFoundError) Unwrap() error            { return e.APIError }
func (e *UnprocessableEntityError) Unwrap() error { return e.APIError }
func (e *InternalServerError) Unwrap() error      { return e.APIError }
func (e *RateLimitError) Unwrap() error           { return e.APIError }
func (e *ResponseValidationError) Unwrap() error  { return e.APIError }

// ConnectionError preserves the underlying transport or body-read failure.
type ConnectionError struct{ Err error }

func (e *ConnectionError) Error() string { return "typesafe: connection error: " + e.Err.Error() }
func (e *ConnectionError) Unwrap() error { return e.Err }

// TimeoutError is a per-attempt timeout, distinct from the caller's context
// deadline. errors.Is and errors.As can inspect its underlying cause.
type TimeoutError struct {
	*ConnectionError
	Timeout time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("typesafe: request timed out (timeout=%s)", e.Timeout)
}
func (e *TimeoutError) Unwrap() error { return e.ConnectionError }

func decodeErrorBody(data []byte) any {
	if len(data) == 0 {
		return nil
	}
	var body any
	if json.Unmarshal(data, &body) == nil {
		return body
	}
	return strings.ToValidUTF8(string(data), "\uFFFD")
}

func errorMessage(body any) string {
	if text, ok := body.(string); ok {
		return text
	}
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := m["error"].(string); ok {
		return s
	}
	if nested, ok := m["error"].(map[string]any); ok {
		if s, ok := nested["message"].(string); ok {
			return s
		}
	}
	if s, ok := m["message"].(string); ok {
		return s
	}
	if s, ok := m["detail"].(string); ok {
		return s
	}
	if nested, ok := m["detail"].(map[string]any); ok {
		if s, ok := nested["message"].(string); ok {
			return s
		}
	}
	if details, ok := m["detail"].([]any); ok {
		var parts []string
		for _, detail := range details {
			entry, ok := detail.(map[string]any)
			if !ok {
				continue
			}
			message, ok := entry["msg"].(string)
			if !ok {
				continue
			}
			var path []string
			if loc, ok := entry["loc"].([]any); ok {
				for _, item := range loc {
					if item == "body" {
						continue
					}
					path = append(path, fmt.Sprint(item))
				}
			}
			if len(path) > 0 {
				message = strings.Join(path, ".") + ": " + message
			}
			parts = append(parts, message)
		}
		return strings.Join(parts, "; ")
	}
	return ""
}

func baseAPIError(response *http.Response, data []byte) *APIError {
	body := decodeErrorBody(data)
	message := errorMessage(body)
	if message == "" {
		if body == nil {
			message = "status code (no body)"
		} else {
			message = strings.ToValidUTF8(string(data), "\uFFFD")
			runes := []rune(message)
			if len(runes) > 200 {
				message = string(runes[:200]) + "…"
			}
		}
	}
	endpoint := ""
	if response.Request != nil {
		u := *response.Request.URL
		u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
		endpoint = response.Request.Method + " " + u.String()
	}
	return &APIError{StatusCode: response.StatusCode, Body: body,
		Headers: response.Header.Clone(), RequestID: response.Header.Get("X-TypeSafe-Request-ID"),
		Endpoint: endpoint, Message: message}
}

func newAPIError(response *http.Response, data []byte) error {
	e := baseAPIError(response, data)
	switch e.StatusCode {
	case 400:
		return &BadRequestError{e}
	case 401:
		return &AuthenticationError{e}
	case 403:
		return &PermissionDeniedError{e}
	case 404:
		return &NotFoundError{e}
	case 422:
		return &UnprocessableEntityError{e}
	case 429:
		delay, ok := parseRetryAfter(e.Headers, time.Now())
		return &RateLimitError{e, delay, ok}
	default:
		if e.StatusCode >= 500 {
			return &InternalServerError{e}
		}
		return e
	}
}

func responseValidationError(response *http.Response, data []byte, path string) error {
	e := baseAPIError(response, data)
	e.Message = fmt.Sprintf("invalid response data at %q", path)
	return &ResponseValidationError{APIError: e, FieldPath: path}
}
