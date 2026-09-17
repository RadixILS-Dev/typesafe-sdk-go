package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testResult = `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{
 "billing":{"type":"noul","noul":0.98},
 "tone":{"type":"choice","choice":"angry","confidence":0.9,"probabilities":{"calm":0.1,"angry":0.9}},
 "urgency":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"low","1":"medium","2":"high"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}
}}`

func newTestClient(t *testing.T, handler http.HandlerFunc, options ...ClientOption) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	base := []ClientOption{WithAPIKey("test-key"), WithBaseURL(server.URL), WithModel(DefaultModel), WithRetryPolicy(RetryPolicy{})}
	client, err := NewClient(append(base, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testQuestions() Questions {
	return Questions{
		"billing": Noul{Instructions: "Is this about billing?"},
		"tone":    Choice{Instructions: "What is the tone?", Criteria: map[string]string{"calm": "", "angry": ""}},
		"urgency": Score{Instructions: "How urgent is this?", Criteria: []string{"low", "medium", "high"}},
	}
}

func jsonEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("JSON mismatch:\ngot  %s\nwant %s", got, want)
	}
}

func TestPublicUsage(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authentication")
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
			t.Error("missing JSON headers")
		}
		if r.Header.Get("X-TypeSafe-SDK") != "typesafe-sdk-go/"+Version || r.Header.Get("X-TypeSafe-Runtime") == "" {
			t.Error("missing SDK headers")
		}
		data, _ := io.ReadAll(r.Body)
		jsonEqual(t, data, `{"state":"I was charged twice. Please help ASAP.","model":"jev-latest","questions":{
		 "billing":{"type":"noul","instructions":"Is this about billing?"},
		 "tone":{"type":"choice","instructions":"What is the tone?","criteria":{"calm":"","angry":""}},
		 "urgency":{"type":"score","instructions":"How urgent is this?","criteria":["low","medium","high"]}}}`)
		w.Header().Set("X-TypeSafe-Request-ID", "req-42")
		fmt.Fprint(w, testResult)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := client.SystemOne(ctx, "I was charged twice. Please help ASAP.", testQuestions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Nouls["billing"].Noul != 0.98 || result.Choices["tone"].Choice != "angry" || result.Scores["urgency"].Score != 1.7 {
		t.Fatalf("wrong grouped answers: %+v", result)
	}
	if result.Answers["urgency"] != result.Scores["urgency"] {
		t.Error("groups must share answer objects")
	}
	if result.Scores["urgency"].Legend[2] != "high" || result.Scores["urgency"].Probabilities[2] != 0.8 {
		t.Error("incorrect integer-keyed score maps")
	}
	if result.Model != DefaultModel || *result.Usage.InputTokens != 12 || *result.Usage.OutputTokens != 3 {
		t.Error("missing model/usage")
	}
	if result.RequestID != "req-42" || result.RawHTTPResponse.StatusCode != 200 {
		t.Error("missing metadata")
	}
	data, _ := io.ReadAll(result.RawHTTPResponse.Body)
	jsonEqual(t, data, testResult)
	data, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, data, testResult) // Derived views and metadata must not leak into JSON.
	var restored SystemOneResponse
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Scores["urgency"].Score != 1.7 {
		t.Fatal("round trip lost grouped answers")
	}
}

func TestConfiguration(t *testing.T) {
	t.Setenv(APIKeyEnv, "  env-key \n")
	t.Setenv(BaseURLEnv, " https://example.test/root/// ")
	t.Setenv(DefaultModelEnv, " env-model ")
	client, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	if client.config.apiKey != "env-key" || client.config.baseURL != "https://example.test/root" || client.config.model != "env-model" {
		t.Fatal("environment resolution failed")
	}
	if client.config.timeout != 10*time.Second || client.config.retry.MaxRetries != 2 {
		t.Fatal("incorrect defaults")
	}
	client, err = NewClient(WithAPIKey("explicit"), WithBaseURL("https://override.test"), WithModel("explicit-model"))
	if err != nil {
		t.Fatal(err)
	}
	if client.config.apiKey != "explicit" || client.config.model != "explicit-model" || client.config.baseURL != "https://override.test" {
		t.Fatal("explicit settings must win")
	}
	t.Setenv(APIKeyEnv, " \t")
	if _, err := NewClient(); err == nil {
		t.Fatal("missing key accepted")
	}
	t.Setenv(BaseURLEnv, " \t")
	t.Setenv(DefaultModelEnv, " \t")
	client, err = NewClient(WithAPIKey("key"))
	if err != nil {
		t.Fatal(err)
	}
	if client.config.baseURL != DefaultBaseURL || client.config.model != DefaultModel {
		t.Fatal("blank environment should be ignored")
	}
	for _, option := range []ClientOption{WithTimeout(0), WithTimeout(-1), WithHTTPClient(nil), WithBaseURL("relative"), WithBaseURL("ftp://example.test"), WithBaseURL("https://user:pass@example.test"), WithBaseURL("https://example.test?q=secret"), WithAPIKey(""), nil} {
		if _, err := NewClient(WithAPIKey("key"), option); err == nil {
			t.Fatal("invalid option accepted")
		}
	}
	httpClient := &http.Client{Timeout: 42 * time.Second}
	client, err = NewClient(WithAPIKey("key"), WithHTTPClient(httpClient))
	if err != nil || client.config.timeout != 42*time.Second {
		t.Fatal("HTTP client timeout not inherited")
	}
	client, err = NewClient(WithAPIKey("key"), WithTimeout(time.Second), WithHTTPClient(httpClient))
	if err != nil || client.config.timeout != time.Second {
		t.Fatal("explicit timeout must win")
	}
	if httpClient.CheckRedirect != nil || httpClient.Timeout != 42*time.Second {
		t.Fatal("caller HTTP client mutated")
	}
}

func TestPerRequestOverridesAndProtectedHeaders(t *testing.T) {
	var bodies [][]byte
	var headers []http.Header
	defaults := http.Header{"X-Default": {"default"}, "authorization": {"wrong"}, "x-typesafe-retry-count": {"99"}}
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		headers = append(headers, r.Header.Clone())
		fmt.Fprint(w, testResult)
	}, WithHeaders(defaults))
	defaults.Set("X-Default", "mutated")
	extra := map[string]any{"state": map[string]any{"replacement": true}, "model": "body-model", "future": nil}
	_, err := client.SystemOne(context.Background(), "original", Questions{"q": Noul{}}, WithRequestModel("call-model"),
		WithRequestHeaders(http.Header{"x-default": {"request"}, "authorization": {"wrong"}, "accept": {"wrong"}, "user-agent": {"wrong"}, "x-typesafe-retry-count": {"99"}}), WithExtraBody(extra))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.SystemOne(context.Background(), "original", Questions{"q": Noul{}})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, bodies[0], `{"state":{"replacement":true},"model":"body-model","future":null,"questions":{"q":{"type":"noul"}}}`)
	jsonEqual(t, bodies[1], `{"state":"original","model":"jev-latest","questions":{"q":{"type":"noul"}}}`)
	if headers[0].Get("X-Default") != "request" || headers[1].Get("X-Default") != "default" {
		t.Fatal("header overrides leaked")
	}
	for _, header := range headers {
		if header.Get("Authorization") != "Bearer test-key" || header.Get("Accept") != "application/json" || header.Get("User-Agent") != "typesafe-sdk-go/"+Version || header.Get("X-TypeSafe-Retry-Count") != "" {
			t.Fatalf("protected headers overwritten: %v", header)
		}
	}
}

func TestModels(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Error("wrong models endpoint")
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) != 0 {
			t.Error("GET sent a body")
		}
		w.Header().Set("X-TypeSafe-Request-ID", "req-models")
		fmt.Fprint(w, `{"models":[{"name":"jev-latest","description":"Fast","release_date":"2026-09-14","future":true}]}`)
	})
	result, err := client.Models.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || result.Models[0].Name != DefaultModel || result.RequestID != "req-models" {
		t.Fatalf("bad models response: %+v", result)
	}
	if !bytes.Contains(result.RawBody, []byte(`"future":true`)) {
		t.Fatal("raw response lost unknown fields")
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached network") })
	var nilScore *Score
	for _, questions := range []Questions{nil, {}, {"q": nil}, {"q": nilScore}, {"q": Score{Criteria: []string{}}}, {"q": &Score{}}, {"q": RawQuestion{}}, {"q": RawQuestion{"type": ""}}, {"q": RawQuestion{"type": "choice"}}, {"q": RawQuestion{"type": "score", "criteria": []any{}}}} {
		if _, err := client.SystemOne(context.Background(), "state", questions); err == nil {
			t.Fatalf("accepted invalid questions: %#v", questions)
		}
	}
	if _, err := client.SystemOne(context.Background(), make(chan int), testQuestions()); err == nil {
		t.Fatal("unserializable state accepted")
	}
	if _, err := client.SystemOne(nil, "state", testQuestions()); err == nil {
		t.Fatal("nil context accepted")
	}
	for _, option := range []RequestOption{nil, WithRequestTimeout(0), WithRequestRetryPolicy(RetryPolicy{MaxRetries: -1})} {
		if _, err := client.SystemOne(context.Background(), "state", testQuestions(), option); err == nil {
			t.Fatal("invalid request option accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.SystemOne(ctx, "state", testQuestions()); !errors.Is(err, context.Canceled) {
		t.Fatalf("wrong cancellation: %v", err)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   any
	}{
		{400, &BadRequestError{}}, {401, &AuthenticationError{}}, {403, &PermissionDeniedError{}},
		{404, &NotFoundError{}}, {422, &UnprocessableEntityError{}}, {429, &RateLimitError{}},
		{500, &InternalServerError{}}, {529, &InternalServerError{}}, {408, &APIError{}}, {302, &APIError{}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-TypeSafe-Request-ID", "req-error")
				w.Header().Set("Retry-After-Ms", "125")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"detail":[{"loc":["body","questions","q"],"msg":"invalid"}]}`)
			})
			_, err := client.Models.List(context.Background())
			if reflect.TypeOf(err) != reflect.TypeOf(tc.want) {
				t.Fatalf("got %T, want %T", err, tc.want)
			}
			var api *APIError
			if !errors.As(err, &api) || api.StatusCode != tc.status || api.RequestID != "req-error" || api.Message != "questions.q: invalid" {
				t.Fatalf("missing error metadata: %v", err)
			}
			if !strings.Contains(err.Error(), "GET ") || !strings.Contains(err.Error(), "req-error") {
				t.Fatal("missing error context")
			}
			if rate, ok := err.(*RateLimitError); ok && (!rate.HasRetryAfter || rate.RetryAfter != 125*time.Millisecond) {
				t.Fatal("missing rate-limit delay")
			}
		})
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	})
	_, err := client.Models.List(context.Background())
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 307 || attempts.Load() != 1 {
		t.Fatalf("redirect followed: %v, attempts %d", err, attempts.Load())
	}
}

func TestConcurrentRequestOverrides(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		fmt.Fprintf(w, `{"model":%q,"usage":{},"answers":{}}`, body.Model)
	})
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			model := fmt.Sprintf("model-%d", i)
			result, err := client.SystemOne(context.Background(), "state", Questions{"q": Noul{}}, WithRequestModel(model))
			if err != nil {
				t.Error(err)
				return
			}
			if result.Model != model {
				t.Errorf("override leaked: %s != %s", result.Model, model)
			}
		}(i)
	}
	wg.Wait()
}
