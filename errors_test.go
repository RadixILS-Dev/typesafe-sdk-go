package typesafe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestErrorEndpointRedactionAndFallback(t *testing.T) {
	request, err := http.NewRequest("GET", "https://user:secret@example.test/v1/models?key=secret#private", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{StatusCode: 500, Header: make(http.Header), Request: request}
	api := baseAPIError(response, nil)
	if api.Endpoint != "GET https://example.test/v1/models" || api.Message != "status code (no body)" {
		t.Fatalf("incorrect error: %v", api)
	}
	api = baseAPIError(response, []byte(`{"unknown":"`+strings.Repeat("x", 300)+`"}`))
	if len([]rune(api.Message)) != 201 || !strings.HasSuffix(api.Message, "…") {
		t.Fatal("fallback message was not truncated")
	}
	if strings.Contains(api.Error(), "secret") || strings.Contains(api.Error(), "private") {
		t.Fatal("URL credentials leaked")
	}
	response.Request = nil
	api = baseAPIError(response, []byte("unstructured failure"))
	if api.Error() != "500 unstructured failure" {
		t.Fatalf("unexpected plain error: %v", api)
	}
}

func TestTransportErrorStrings(t *testing.T) {
	cause := errors.New("failed")
	connection := &ConnectionError{Err: cause}
	if !strings.Contains(connection.Error(), "failed") {
		t.Fatal("connection error omitted cause")
	}
	timeout := &TimeoutError{ConnectionError: connection, Timeout: time.Second}
	if !strings.Contains(timeout.Error(), "1s") || !errors.Is(timeout, cause) {
		t.Fatal("timeout omitted duration/cause")
	}
}

func TestLoggingDoesNotExposeBodiesOrHeaders(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "cookie-secret")
		if attempts.Add(1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, "response-secret")
			return
		}
		fmt.Fprint(w, testResult)
	}, WithLogger(logger), WithRetryPolicy(fastRetry()), WithHeaders(http.Header{"X-Secret": {"header-secret"}}))
	if _, err := client.SystemOne(context.Background(), "body-secret", Questions{"q": Noul{}}); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	if !strings.Contains(text, "typesafe retry") || !strings.Contains(text, "typesafe response") {
		t.Fatalf("metadata not logged: %s", text)
	}
	for _, secret := range []string{"test-key", "cookie-secret", "response-secret", "header-secret", "body-secret"} {
		if strings.Contains(text, secret) {
			t.Errorf("logs leaked %q", secret)
		}
	}
}
