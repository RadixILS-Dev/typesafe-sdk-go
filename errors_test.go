package typesafe

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestErrorEndpointRedactionAndFallback(t *testing.T) {
	request, err := http.NewRequest("GET", "https://user:secret@example.test/v1/models?key=secret#private", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{StatusCode: 500, Header: make(http.Header), Request: request}
	api := newAPIError(response, nil)
	if api.Endpoint != "GET https://example.test/v1/models" || api.Message != "status code (no body)" {
		t.Fatalf("incorrect error: %v", api)
	}
	api = newAPIError(response, []byte(`{"unknown":"`+strings.Repeat("x", 300)+`"}`))
	if len([]rune(api.Message)) != 201 || !strings.HasSuffix(api.Message, "…") {
		t.Fatal("fallback message was not truncated")
	}
	if strings.Contains(api.Error(), "secret") || strings.Contains(api.Error(), "private") {
		t.Fatal("URL credentials leaked")
	}
	response.Request = nil
	api = newAPIError(response, []byte("unstructured failure"))
	if api.Error() != "500 unstructured failure" {
		t.Fatalf("unexpected plain error: %v", api)
	}
}

func TestResponseErrorPreservesCauseAndRequestID(t *testing.T) {
	_, err := decodeSystemOne([]byte(`{"model":`))
	err = withRequestID(err, "req-invalid")
	var invalid *ResponseError
	var syntax *json.SyntaxError
	if !errors.As(err, &invalid) || !errors.As(err, &syntax) {
		t.Fatalf("JSON error lost: %v", err)
	}
	if invalid.RequestID != "req-invalid" || !strings.Contains(err.Error(), "req-invalid") {
		t.Fatalf("request ID lost: %v", err)
	}
}
