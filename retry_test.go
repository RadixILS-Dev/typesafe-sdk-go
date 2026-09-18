package typesafe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryStatuses(t *testing.T) {
	for _, tc := range []struct{ status, attempts int }{
		{408, 3}, {429, 3}, {500, 3}, {529, 3}, {599, 3}, {600, 1}, {400, 1}, {401, 1}, {403, 1}, {404, 1}, {409, 1}, {422, 1},
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
				w.Header().Set("Retry-After-Ms", "0")
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `{"message":"attempt %d"}`, attempt)
			})
			_, err := client.ListModels(context.Background())
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
		if attempts.Add(1)%3 != 0 {
			w.Header().Set("Retry-After-Ms", "0")
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, testResult)
	}, WithModel("custom"))
	for i := 0; i < 2; i++ {
		result, err := client.SystemOne(context.Background(), "state", Questions{"q": Noul{}})
		if err != nil || result == nil {
			t.Fatalf("failed recovery: %v", err)
		}
	}
	if attempts.Load() != 6 {
		t.Fatal("retry state leaked across calls")
	}
}

func TestMaxAttempts(t *testing.T) {
	for _, maxAttempts := range []int{1, 2, 4} {
		var attempts atomic.Int32
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			w.Header().Set("Retry-After-Ms", "0")
			w.WriteHeader(503)
		}, WithMaxAttempts(maxAttempts))
		if _, err := client.ListModels(context.Background()); err == nil {
			t.Fatal("expected API error")
		}
		if attempts.Load() != int32(maxAttempts) {
			t.Fatalf("wanted %d attempts, got %d", maxAttempts, attempts.Load())
		}
	}
}

func TestRetryBudget(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	})
	_, err := client.ListModels(context.Background())
	var api *APIError
	if !errors.As(err, &api) || attempts.Load() != 1 {
		t.Fatalf("retry exceeding budget not prevented: %v", err)
	}
}

func TestCancellationDuringRetryWait(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(429)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.ListModels(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("deadline did not stop retry: %v", err)
	}
}

func TestWaitForRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait ignored cancellation: %v", err)
	}
	if err := waitForRetry(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNativeTransportErrorsAndTimeouts(t *testing.T) {
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
			client, err := NewClient(WithAPIKey("key"), WithBaseURL("https://example.test"), WithHTTPClient(&http.Client{Transport: transport, Timeout: time.Millisecond}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ListModels(context.Background())
			if attempts.Load() != 3 {
				t.Fatalf("transport error not retried: %v", err)
			}
			if timeout {
				var networkError net.Error
				if !errors.As(err, &networkError) || !networkError.Timeout() || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("native timeout lost: %v", err)
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
	client, err := NewClient(WithAPIKey("key"), WithBaseURL(DefaultBaseURL), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = client.ListModels(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("caller deadline retried: %v", err)
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
		return &http.Response{StatusCode: 200, Header: http.Header{"Retry-After-Ms": {"0"}}, Body: body, Request: r}, nil
	})
	client, err := NewClient(WithAPIKey("key"), WithBaseURL(DefaultBaseURL), WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListModels(context.Background())
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

func TestBackoff(t *testing.T) {
	for index, cap := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second} {
		for i := 0; i < 10; i++ {
			delay := retryDelay(index, nil, time.Now())
			if delay > cap || delay < cap*3/4 {
				t.Fatalf("backoff %d out of bounds: %s", index, delay)
			}
		}
	}
	if delay := retryDelay(1000000, nil, time.Now()); delay > 5*time.Second || delay < 3750*time.Millisecond {
		t.Fatal("backoff did not cap safely")
	}
}
