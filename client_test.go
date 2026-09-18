package typesafe

import (
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
	defaults := []ClientOption{WithAPIKey("test-key"), WithBaseURL(server.URL), WithModel(DefaultModel)}
	client, err := NewClient(append(defaults, options...)...)
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
		t.Error(err)
		return
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Error(err)
		return
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("JSON mismatch:\ngot  %s\nwant %s", got, want)
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
	if result.Scores["urgency"].Legend[2] != "high" || result.Scores["urgency"].Probabilities[2] != 0.8 {
		t.Error("incorrect integer-keyed score maps")
	}
	if result.Model != DefaultModel || *result.Usage.InputTokens != 12 || *result.Usage.OutputTokens != 3 {
		t.Error("missing model/usage")
	}
	if result.RequestID != "req-42" {
		t.Error("missing request ID")
	}
	jsonEqual(t, result.RawAnswers["billing"], `{"type":"noul","noul":0.98}`)
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
	result, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 1 || result.Models[0].Name != DefaultModel || result.RequestID != "req-models" {
		t.Fatalf("bad models response: %+v", result)
	}
}

func TestLocalFailuresDoNotSend(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached network") })
	if _, err := client.SystemOne(context.Background(), make(chan int), testQuestions()); err == nil {
		t.Fatal("unserializable state accepted")
	}
	if _, err := client.SystemOne(nil, "state", testQuestions()); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.SystemOne(ctx, "state", testQuestions()); !errors.Is(err, context.Canceled) {
		t.Fatalf("wrong cancellation: %v", err)
	}
}

func TestQuestionValidationBelongsToAPI(t *testing.T) {
	var calls atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(422)
		fmt.Fprint(w, `{"detail":"Invalid question"}`)
	})
	var nilScore *Score
	inputs := []Questions{nil, {}, {"q": nil}, {"q": nilScore}, {"q": Score{Criteria: []string{}}}, {"q": map[string]any{}}}
	for _, questions := range inputs {
		_, err := client.SystemOne(context.Background(), "state", questions)
		var api *APIError
		if !errors.As(err, &api) || api.StatusCode != 422 {
			t.Fatalf("expected server validation error, got %v", err)
		}
	}
	if calls.Load() != int32(len(inputs)) {
		t.Fatal("questions were not sent exactly once each")
	}
}

func TestHTTPErrorMetadata(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422, 429, 500, 529, 408, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-TypeSafe-Request-ID", "req-error")
				w.Header().Set("Retry-After-Ms", "125")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"detail":[{"loc":["body","questions","q"],"msg":"invalid"}]}`)
			}, WithMaxAttempts(1))
			_, err := client.ListModels(context.Background())
			var api *APIError
			if !errors.As(err, &api) || api.StatusCode != status || api.RequestID != "req-error" || api.Message != "questions.q: invalid" {
				t.Fatalf("missing error metadata: %v", err)
			}
			if !strings.Contains(err.Error(), "GET ") || !strings.Contains(err.Error(), "req-error") {
				t.Fatal("missing error context")
			}
			if api.Headers.Get("Retry-After-Ms") != "125" {
				t.Fatal("missing rate-limit header")
			}
		})
	}
}

func TestRedirectPolicy(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprint(custom), func(t *testing.T) {
			var attempts atomic.Int32
			var options []ClientOption
			if custom {
				options = append(options, WithHTTPClient(&http.Client{}))
			}
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				if r.URL.Path == "/v1/models" {
					http.Redirect(w, r, "/redirected", 307)
					return
				}
				fmt.Fprint(w, `{"models":[]}`)
			}, options...)
			_, err := client.ListModels(context.Background())
			if custom {
				if err != nil || attempts.Load() != 2 {
					t.Fatalf("supplied redirect policy ignored: %v", err)
				}
			} else {
				var api *APIError
				if !errors.As(err, &api) || api.StatusCode != 307 || attempts.Load() != 1 {
					t.Fatalf("default followed redirect: %v", err)
				}
			}
		})
	}
}

func TestConcurrentCalls(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		fmt.Fprintf(w, `{"model":%q,"usage":{},"answers":{}}`, body.State)
	})
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			state := fmt.Sprintf("state-%d", i)
			result, err := client.SystemOne(context.Background(), state, Questions{"q": Noul{}})
			if err != nil {
				t.Error(err)
				return
			}
			if result.Model != state {
				t.Errorf("request state leaked: %s != %s", result.Model, state)
			}
		}(i)
	}
	wg.Wait()
}
