package typesafe

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RetryPolicy controls retries after the initial request. Its zero value disables
// retries. Use DefaultRetryPolicy to obtain the Python SDK's defaults, then
// modify individual fields. Predicate can add retryable errors to built-in rules;
// it must be safe for concurrent calls. Caller context cancellation is never retried.
type RetryPolicy struct {
	MaxRetries         int
	BackoffInitial     time.Duration
	BackoffMax         time.Duration
	BackoffJitter      float64
	HTTPStatuses       []int
	RespectRetryAfter  bool
	APIConnectionError bool
	APITimeoutError    bool
	Predicate          func(error) bool
	// Timeout is the retry scheduling budget, including time spent on attempts.
	// Zero disables the budget. It does not interrupt an in-flight request; use
	// the caller's context for a hard deadline. A retry is not scheduled if its
	// delay would reach or exceed the remaining budget.
	Timeout time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	statuses := []int{408, 429}
	for i := 500; i < 600; i++ {
		statuses = append(statuses, i)
	}
	return RetryPolicy{
		MaxRetries: 2, BackoffInitial: 500 * time.Millisecond, BackoffMax: 5 * time.Second,
		BackoffJitter: 0.25, HTTPStatuses: statuses, RespectRetryAfter: true,
		APIConnectionError: true, APITimeoutError: true, Timeout: 30 * time.Second,
	}
}

func (p RetryPolicy) clone() RetryPolicy {
	p.HTTPStatuses = append([]int(nil), p.HTTPStatuses...)
	return p
}
func (p RetryPolicy) validate() error {
	if p.MaxRetries < 0 {
		return fmt.Errorf("typesafe: max retries must not be negative")
	}
	if p.BackoffInitial < 0 || p.BackoffMax < 0 || p.Timeout < 0 {
		return fmt.Errorf("typesafe: retry durations must not be negative")
	}
	if math.IsNaN(p.BackoffJitter) || p.BackoffJitter < 0 || p.BackoffJitter > 1 {
		return fmt.Errorf("typesafe: backoff jitter must be between zero and one")
	}
	return nil
}

func (p RetryPolicy) retryable(err error) bool {
	var timeout *TimeoutError
	var connection *ConnectionError
	var api *APIError
	builtin := false
	switch {
	case errors.As(err, &timeout):
		builtin = p.APITimeoutError
	case errors.As(err, &connection):
		builtin = p.APIConnectionError
	case errors.As(err, &api):
		for _, status := range p.HTTPStatuses {
			if api.StatusCode == status {
				builtin = true
				break
			}
		}
	}
	return builtin || (p.Predicate != nil && p.Predicate(err))
}

// retryDelay uses a zero-based retry index: the first retry is index zero.
func (p RetryPolicy) retryDelay(index int, err error) time.Duration {
	var api *APIError
	if p.RespectRetryAfter && errors.As(err, &api) {
		if delay, ok := parseRetryAfter(api.Headers, time.Now()); ok {
			return delay
		}
	}
	if p.BackoffInitial == 0 || p.BackoffMax == 0 {
		return 0
	}
	delay := min(p.BackoffInitial, p.BackoffMax)
	for i := 0; i < index && delay < p.BackoffMax; i++ {
		if delay > p.BackoffMax/2 {
			delay = p.BackoffMax
		} else {
			delay *= 2
		}
	}
	// Subtract rather than add jitter, matching the Python SDK.
	return delay - time.Duration(float64(delay)*rand.Float64()*p.BackoffJitter)
}

func parseRetryAfter(headers http.Header, now time.Time) (time.Duration, bool) {
	for _, entry := range []struct {
		key  string
		unit time.Duration
	}{
		{"Retry-After-Ms", time.Millisecond}, {"Retry-After", time.Second},
	} {
		values := headers.Values(entry.key)
		if len(values) == 0 {
			continue
		}
		raw := strings.TrimSpace(values[0])
		if raw == "" {
			raw = "0"
		}
		if value, err := strconv.ParseFloat(raw, 64); err == nil {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			if value < 0 {
				if entry.key == "Retry-After" {
					return 0, false
				}
				continue
			}
			nanos := value * float64(entry.unit)
			// Saturation avoids overflow turning a huge server delay into an
			// immediate retry. The budget/context will normally prevent it.
			if nanos >= float64(math.MaxInt64) {
				return time.Duration(math.MaxInt64), true
			}
			return time.Duration(nanos), true
		}
		if entry.key == "Retry-After" {
			if date, err := http.ParseTime(raw); err == nil {
				return max(0, date.Sub(now)), true
			}
		}
	}
	return 0, false
}
