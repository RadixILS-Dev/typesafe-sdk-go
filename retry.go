package typesafe

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Only HTTP/transport failures reach this function; encoding and response
// validation errors are outside the retry loop.
func retryable(err error) bool {
	var api *APIError
	if errors.As(err, &api) {
		return api.StatusCode == 408 || api.StatusCode == 429 || (api.StatusCode >= 500 && api.StatusCode < 600)
	}
	return true
}

// The first retry has index zero. Capping the exponent avoids overflow.
func retryDelay(index int, headers http.Header, now time.Time) time.Duration {
	if delay, ok := parseRetryAfter(headers, now); ok {
		return delay
	}
	delay := min(500*time.Millisecond<<min(index, 4), 5*time.Second)
	return delay - time.Duration(float64(delay)*rand.Float64()*0.25)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
