package typesafe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastRetry() RetryPolicy {
	policy := DefaultRetryPolicy()
	policy.BackoffInitial = 0
	return policy
}

func TestRetryStatuses(t *testing.T) {
	for _, tc := range []struct{ status, attempts int }{
		{408, 3}, {429, 3}, {500, 3}, {529, 3}, {599, 3}, {400, 1}, {401, 1}, {403, 1}, {404, 1}, {409, 1}, {422, 1},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var attempts atomic.Int32
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempt := int(attempts.Add(1))
				want := ""
				if attempt > 1 {
					want = fmt.Sprint(attempt - 1)
				}
				if r.Header.Get("X-TypeSafe-Retry-Count") != want {
					t.Errorf("incorrect retry header on attempt %d", attempt)
				}
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"message":"attempt %d"}`, attempt)
			}, WithRetryPolicy(fastRetry()))
			_, err := client.Models.List(context.Background())
			var api *APIError
			if !errors.As(err, &api) || attempts.Load() != int32(tc.attempts) || api.Message != fmt.Sprintf("attempt %d", tc.attempts) {
				t.Fatalf("incorrect final error: %v (%d attempts)", err, attempts.Load())
			}
		})
	}
}

func TestRetryRecoveryAndReplay(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		jsonEqual(t, data, `{"state":"state","model":"custom","questions":{"q":{"type":"noul"}}}`)
		if attempts.Add(1) < 3 {
			w.Header().Set("Retry-After-Ms", "0")
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, testResult)
	}, WithRetryPolicy(DefaultRetryPolicy()))
	result, err := client.SystemOne(context.Background(), "state", Questions{"q": Noul{}}, WithRequestModel("custom"))
	if err != nil || result == nil || attempts.Load() != 3 {
		t.Fatalf("failed recovery: %v", err)
	}
}

func TestPerCallRetryOverride(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); w.WriteHeader(503) }, WithRetryPolicy(fastRetry()))
	client.Models.List(context.Background(), WithRequestRetryPolicy(RetryPolicy{}))
	if attempts.Load() != 1 {
		t.Fatal("request did not disable retries")
	}
	client.Models.List(context.Background())
	if attempts.Load() != 4 {
		t.Fatal("request override changed client policy")
	}
}

func TestRetryBudget(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}, WithRetryPolicy(DefaultRetryPolicy()))
	_, err := client.Models.List(context.Background())
	var rate *RateLimitError
	if !errors.As(err, &rate) || attempts.Load() != 1 {
		t.Fatalf("retry exceeding budget not prevented: %v", err)
	}
}

func TestCancellationDuringRetryWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	policy := DefaultRetryPolicy()
	policy.Timeout = 0
	// A status outside built-in retry rules makes Predicate run, guaranteeing
	// cancellation occurs after the response but before the retry wait.
	policy.Predicate = func(error) bool { cancel(); return true }
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(418)
	}, WithRetryPolicy(policy))
	_, err := client.Models.List(ctx)
	if !errors.Is(err, context.Canceled) || attempts.Load() != 1 {
		t.Fatalf("cancel did not stop retry: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportErrorsAndTimeout(t *testing.T) {
	failure := errors.New("connection failed")
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			var attempts atomic.Int32
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts.Add(1)
				if timeout {
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return nil, failure
			})
			client, err := NewClient(WithAPIKey("key"), WithBaseURL("https://example.test"), WithHTTPClient(&http.Client{Transport: transport}), WithTimeout(time.Millisecond), WithRetryPolicy(fastRetry()))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Models.List(context.Background())
			var connection *ConnectionError
			if !errors.As(err, &connection) || attempts.Load() != 3 {
				t.Fatalf("connection error not retried: %v", err)
			}
			if timeout {
				var timedOut *TimeoutError
				if !errors.As(err, &timedOut) || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("timeout classification lost: %v", err)
				}
			} else if !errors.Is(err, failure) {
				t.Fatal("underlying error lost")
			}
		})
	}
}

func TestCallerDeadlineNotRetried(t *testing.T) {
	var attempts atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	client, err := NewClient(WithAPIKey("key"), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = client.Models.List(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("caller deadline retried: %v", err)
	}
}

func TestPerCallTimeout(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 100*time.Millisecond {
			t.Error("call timeout not applied")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[]}`)), Request: r}, nil
	})
	client, err := NewClient(WithAPIKey("key"), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Models.List(context.Background(), WithRequestTimeout(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if client.config.timeout != DefaultTimeout {
		t.Fatal("request timeout changed client")
	}
}

type failingBody struct{ closed bool }

func (*failingBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (b *failingBody) Close() error           { b.closed = true; return nil }

func TestBodyReadFailureClosesAndRetries(t *testing.T) {
	var bodies []*failingBody
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := &failingBody{}
		bodies = append(bodies, body)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
	})
	client, err := NewClient(WithAPIKey("key"), WithHTTPClient(&http.Client{Transport: transport}), WithRetryPolicy(fastRetry()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Models.List(context.Background())
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(bodies) != 3 {
		t.Fatalf("body error not retried: %v", err)
	}
	for _, body := range bodies {
		if !body.closed {
			t.Fatal("network body leaked")
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		ms, seconds string
		want        time.Duration
		valid       bool
	}{
		{"125", "2", 125 * time.Millisecond, true}, {"0", "50", 0, true},
		{"-1", "0.5", 500 * time.Millisecond, true}, {"invalid", "2", 2 * time.Second, true},
		{"NaN", "-1", 0, false}, {"Inf", "bad", 0, false},
		{"bad", now.Add(3 * time.Second).Format(http.TimeFormat), 3 * time.Second, true},
		{"bad", now.Add(-time.Second).Format(http.TimeFormat), 0, true},
		{"", "1", 0, true}, {"1e30", "1", time.Duration(math.MaxInt64), true},
	} {
		got, valid := parseRetryAfter(http.Header{"Retry-After-Ms": {tc.ms}, "Retry-After": {tc.seconds}}, now)
		if got != tc.want || valid != tc.valid {
			t.Errorf("%+v: got %s, %v", tc, got, valid)
		}
	}
	if _, ok := parseRetryAfter(nil, now); ok {
		t.Fatal("absent header accepted")
	}
}

func TestRetryPolicyValidationAndBackoff(t *testing.T) {
	for _, policy := range []RetryPolicy{
		{MaxRetries: -1}, {BackoffInitial: -1}, {BackoffMax: -1}, {Timeout: -1},
		{BackoffJitter: -0.1}, {BackoffJitter: 1.1}, {BackoffJitter: math.NaN()}, {BackoffJitter: math.Inf(1)},
	} {
		if policy.validate() == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
	policy := DefaultRetryPolicy()
	for index, cap := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second} {
		for i := 0; i < 10; i++ {
			delay := policy.retryDelay(index, errors.New("failure"))
			if delay > cap || delay < cap*3/4 {
				t.Fatalf("backoff %d out of bounds: %s", index, delay)
			}
		}
	}
	policy.BackoffJitter = 0
	if policy.retryDelay(1000000, errors.New("failure")) != 5*time.Second {
		t.Fatal("backoff did not cap safely")
	}
}
