package typesafe

import (
	"net/http"
	"testing"
	"time"
)

func TestConfiguration(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "  env-key \n")
	t.Setenv("TYPESAFE_BASE_URL", " https://example.test/root/// ")
	t.Setenv("TYPESAFE_DEFAULT_MODEL", " env-model ")
	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	if client.config.APIKey != "env-key" || client.config.BaseURL != "https://example.test/root" || client.config.Model != "env-model" {
		t.Fatal("environment resolution failed")
	}
	if client.config.HTTPClient.Timeout != 10*time.Second || client.config.MaxAttempts != 3 {
		t.Fatal("incorrect defaults")
	}
	client, err = NewClient(WithAPIKey("explicit"), WithBaseURL("https://override.test"), WithModel("explicit-model"), WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	if client.config.APIKey != "explicit" || client.config.Model != "explicit-model" || client.config.BaseURL != "https://override.test" || client.config.MaxAttempts != 1 {
		t.Fatal("explicit settings not preserved")
	}
	if _, err := NewClient(WithAPIKey("")); err == nil {
		t.Fatal("explicit empty key fell back to environment")
	}
	t.Setenv("TYPESAFE_API_KEY", " \t")
	if _, err := NewClient(); err == nil {
		t.Fatal("missing key accepted")
	}
	t.Setenv("TYPESAFE_BASE_URL", " \t")
	t.Setenv("TYPESAFE_DEFAULT_MODEL", " \t")
	client, err = NewClient(WithAPIKey("key"))
	if err != nil {
		t.Fatal(err)
	}
	if client.config.BaseURL != DefaultBaseURL || client.config.Model != DefaultModel {
		t.Fatal("blank environment should be ignored")
	}
	for _, option := range []ClientOption{
		WithMaxAttempts(-1), WithMaxAttempts(0), WithBaseURL("relative"),
		WithBaseURL("ftp://example.test"), WithBaseURL("https://user:pass@example.test"),
		WithBaseURL("https://example.test?q=secret"), WithBaseURL("http://[invalid"),
		WithBaseURL(""), WithHTTPClient(nil), nil,
	} {
		if _, err := NewClient(WithAPIKey("key"), option); err == nil {
			t.Fatal("invalid option accepted")
		}
	}
}

func TestOptionsAreOrderedAndReusable(t *testing.T) {
	options := []ClientOption{WithAPIKey("key"), WithBaseURL(DefaultBaseURL), WithModel("first"), WithMaxAttempts(2)}
	first, err := NewClient(append(options, WithModel("last"), WithMaxAttempts(1))...)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewClient(options...)
	if err != nil {
		t.Fatal(err)
	}
	if first.config.Model != "last" || first.config.MaxAttempts != 1 {
		t.Fatal("last option did not win")
	}
	if second.config.Model != "first" || second.config.MaxAttempts != 2 {
		t.Fatal("options leaked settings between clients")
	}
	if first.config.HTTPClient == second.config.HTTPClient {
		t.Fatal("default HTTP clients should be independent")
	}
}

func TestSuppliedHTTPClientIsUsedUnchanged(t *testing.T) {
	for _, timeout := range []time.Duration{0, 42 * time.Second} {
		httpClient := &http.Client{Timeout: timeout}
		client, err := NewClient(WithAPIKey("key"), WithBaseURL(DefaultBaseURL), WithHTTPClient(httpClient))
		if err != nil {
			t.Fatal(err)
		}
		if client.config.HTTPClient != httpClient {
			t.Fatal("HTTP client was replaced")
		}
		if httpClient.Timeout != timeout || httpClient.CheckRedirect != nil {
			t.Fatal("supplied HTTP settings changed")
		}
	}
}
