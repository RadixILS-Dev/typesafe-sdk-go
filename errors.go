package typesafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// APIError describes an unsuccessful HTTP response. Use errors.As to inspect it
// and StatusCode to distinguish authentication, rate limits, and other failures.
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

// ResponseError identifies missing or malformed required data in a successful
// response. Err preserves the underlying JSON error when available.
type ResponseError struct {
	FieldPath string
	RequestID string
	Err       error
}

func (e *ResponseError) Error() string {
	message := fmt.Sprintf("typesafe: invalid response data at %q", e.FieldPath)
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	if e.RequestID != "" {
		message += " (request_id=" + e.RequestID + ")"
	}
	return message
}
func (e *ResponseError) Unwrap() error { return e.Err }

func withRequestID(err error, requestID string) error {
	var invalid *ResponseError
	if errors.As(err, &invalid) {
		invalid.RequestID = requestID
	}
	return err
}

func newAPIError(response *http.Response, data []byte) *APIError {
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
	if response.Request != nil && response.Request.URL != nil {
		u := *response.Request.URL
		u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
		endpoint = response.Request.Method + " " + u.String()
	}
	return &APIError{StatusCode: response.StatusCode, Body: body,
		Headers: response.Header.Clone(), RequestID: response.Header.Get("X-TypeSafe-Request-ID"),
		Endpoint: endpoint, Message: message}
}

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
	for _, key := range []string{"error", "message", "detail"} {
		if text, ok := m[key].(string); ok {
			return text
		}
		if nested, ok := m[key].(map[string]any); ok {
			if text, ok := nested["message"].(string); ok {
				return text
			}
		}
	}
	if details, ok := m["detail"].([]any); ok {
		return validationMessages(details)
	}
	return ""
}

func validationMessages(details []any) string {
	var messages []string
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
		if location, ok := entry["loc"].([]any); ok {
			for _, item := range location {
				if item != "body" {
					path = append(path, fmt.Sprint(item))
				}
			}
		}
		if len(path) > 0 {
			message = strings.Join(path, ".") + ": " + message
		}
		messages = append(messages, message)
	}
	return strings.Join(messages, "; ")
}
