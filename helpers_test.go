package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestPick(t *testing.T) {
	for _, selected := range []string{" €42.00 ", NoneChoice} {
		t.Run(selected, func(t *testing.T) {
			candidates := []string{" €42.00 ", "USD 1,234.50", " €42.00 "}
			original := append([]string(nil), candidates...)
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				jsonEqual(t, body, `{"state":{"document":"Amounts: €42.00 and USD 1,234.50"},"model":"helper-model","questions":{"q":{
				 "type":"choice","instructions":{"question":"Which amount is due?","rules":["Do not normalize the amount."]},
				 "criteria":{" €42.00 ":null,"USD 1,234.50":null,"none":"None of these is the requested value."}}}}`)
				fmt.Fprintf(w, `{"model":"helper-model","usage":{},"answers":{"q":{"type":"choice","choice":%q,"confidence":0.8,"probabilities":{%q:0.8,"USD 1,234.50":0.2}}}}`, selected, selected)
			}, WithModel("helper-model"))
			answer, err := client.Pick(context.Background(),
				map[string]any{"document": "Amounts: €42.00 and USD 1,234.50"},
				map[string]any{"question": "Which amount is due?", "rules": []string{"Do not normalize the amount."}}, candidates)
			if err != nil {
				t.Fatal(err)
			}
			if answer.Choice != selected || answer.Confidence != 0.8 || answer.Probabilities[selected] != 0.8 {
				t.Fatalf("incorrect answer: %+v", answer)
			}
			if !reflect.DeepEqual(candidates, original) {
				t.Fatal("candidate slice was mutated")
			}
		})
	}
}

func TestClassify(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		jsonEqual(t, body, `{"state":"Amount due: $42","model":"jev-latest","questions":{"q":{
		 "type":"choice","instructions":"Which currency?","criteria":{"USD":null,"EUR":null,"none":null}}}}`)
		fmt.Fprint(w, `{"model":"jev-latest","usage":{},"answers":{"q":{"type":"choice","choice":"USD","confidence":0.9,"probabilities":{"USD":0.9,"EUR":0.1,"none":0}}}}`)
	})
	answer, err := client.Classify(context.Background(), "Amount due: $42", "Which currency?", []string{"USD", "EUR", NoneChoice})
	if err != nil {
		t.Fatal(err)
	}
	if answer.Choice != "USD" || answer.Confidence != 0.9 || answer.Probabilities["EUR"] != 0.1 {
		t.Fatalf("incorrect answer: %+v", answer)
	}
}

func TestIsTrue(t *testing.T) {
	for _, probability := range []float64{0, 0.37, 1} {
		t.Run(fmt.Sprint(probability), func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				jsonEqual(t, body, `{"state":"Please help ASAP","model":"jev-latest","questions":{"q":{"type":"noul","instructions":"Is this urgent?"}}}`)
				fmt.Fprintf(w, `{"model":"jev-latest","usage":{},"answers":{"q":{"type":"noul","noul":%g}}}`, probability)
			})
			value, err := client.IsTrue(context.Background(), "Please help ASAP", "Is this urgent?")
			if err != nil {
				t.Fatal(err)
			}
			if value != probability {
				t.Fatalf("got %g, want %g", value, probability)
			}
		})
	}
}

func TestPickRejectsReservedCandidate(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("reserved candidate reached network") })
	if _, err := client.Pick(context.Background(), "text", "Which value?", []string{NoneChoice}); err == nil {
		t.Fatal("reserved candidate accepted")
	}
}

func TestEmptyCandidates(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		jsonEqual(t, body, `{"state":"text","model":"jev-latest","questions":{"q":{"type":"choice","instructions":"Which value?","criteria":{"none":"None of these is the requested value."}}}}`)
		fmt.Fprint(w, `{"model":"jev-latest","usage":{},"answers":{"q":{"type":"choice","choice":"none","confidence":1,"probabilities":{"none":1}}}}`)
	})
	answer, err := client.Pick(context.Background(), "text", "Which value?", nil)
	if err != nil || answer.Choice != NoneChoice {
		t.Fatalf("empty pick: %+v, %v", answer, err)
	}

	client = newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		jsonEqual(t, body, `{"state":"text","model":"jev-latest","questions":{"q":{"type":"choice","instructions":"Which label?","criteria":{}}}}`)
		w.WriteHeader(422)
		fmt.Fprint(w, `{"detail":"No options"}`)
	})
	_, err = client.Classify(context.Background(), "text", "Which label?", nil)
	var api *APIError
	if !errors.As(err, &api) || api.StatusCode != 422 {
		t.Fatalf("expected server validation error: %v", err)
	}
}

func TestHelpersRejectUnexpectedAnswers(t *testing.T) {
	cases := []struct {
		name      string
		call      func(*Client) error
		wrongType string
	}{
		{"pick", func(c *Client) error {
			_, err := c.Pick(context.Background(), "text", "Which value?", []string{"value"})
			return err
		}, `{"type":"noul","noul":0.5}`},
		{"classify", func(c *Client) error {
			_, err := c.Classify(context.Background(), "text", "Which label?", []string{"value"})
			return err
		}, `{"type":"noul","noul":0.5}`},
		{"is_true", func(c *Client) error { _, err := c.IsTrue(context.Background(), "text", "True?"); return err }, `{"type":"choice","choice":"value","confidence":1,"probabilities":{"value":1}}`},
	}
	for _, tc := range cases {
		for _, answers := range []string{`{}`, `{"other":{"type":"noul","noul":0.5}}`, `{"q":{"type":"future"}}`, `{"q":` + tc.wrongType + `}`} {
			t.Run(tc.name+"/"+answers, func(t *testing.T) {
				var attempts atomic.Int32
				client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
					attempts.Add(1)
					w.Header().Set("X-TypeSafe-Request-ID", "req-helper")
					fmt.Fprintf(w, `{"model":"jev-latest","usage":{},"answers":%s}`, answers)
				})
				err := tc.call(client)
				var invalid *ResponseError
				if !errors.As(err, &invalid) || invalid.FieldPath != "answers.q" || invalid.RequestID != "req-helper" {
					t.Fatalf("incorrect error: %v", err)
				}
				if attempts.Load() != 1 {
					t.Fatal("unexpected answer was retried")
				}
			})
		}
	}
}

func TestHelpersRejectUnlistedChoice(t *testing.T) {
	for _, pick := range []bool{false, true} {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-TypeSafe-Request-ID", "req-unlisted")
			fmt.Fprint(w, `{"model":"jev-latest","usage":{},"answers":{"q":{"type":"choice","choice":"invented","confidence":1,"probabilities":{"invented":1}}}}`)
		})
		var err error
		if pick {
			_, err = client.Pick(context.Background(), "text", "Which value?", []string{"value"})
		} else {
			_, err = client.Classify(context.Background(), "text", "Which label?", []string{"value"})
		}
		var invalid *ResponseError
		if !errors.As(err, &invalid) || invalid.FieldPath != "answers.q.choice" || invalid.RequestID != "req-unlisted" {
			t.Fatalf("unlisted choice was not rejected: %v", err)
		}
	}
}

func TestHelperErrorPropagation(t *testing.T) {
	calls := map[string]func(*Client, context.Context) error{
		"pick": func(c *Client, ctx context.Context) error {
			_, err := c.Pick(ctx, "text", "Which value?", []string{"value"})
			return err
		},
		"classify": func(c *Client, ctx context.Context) error {
			_, err := c.Classify(ctx, "text", "Which label?", []string{"value"})
			return err
		},
		"is_true": func(c *Client, ctx context.Context) error { _, err := c.IsTrue(ctx, "text", "True?"); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(401)
				fmt.Fprint(w, `{"message":"Invalid API key"}`)
			})
			var api *APIError
			if err := call(client, context.Background()); !errors.As(err, &api) || api.StatusCode != 401 {
				t.Fatalf("API error lost: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := call(client, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("context error lost: %v", err)
			}
			if attempts.Load() != 1 {
				t.Fatal("canceled call reached network")
			}
		})
	}
}

func TestHelperUsesClientRetryConfiguration(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After-Ms", "0")
			w.WriteHeader(503)
			return
		}
		if r.Header.Get("X-TypeSafe-Retry-Count") != "1" {
			t.Error("missing retry count")
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "jev-latest", "usage": map[string]any{}, "answers": map[string]any{"q": map[string]any{"type": "noul", "noul": 0.8}}})
	}, WithMaxAttempts(2))
	value, err := client.IsTrue(context.Background(), "text", "True?")
	if err != nil || value != 0.8 || attempts.Load() != 2 {
		t.Fatalf("retry failed: value=%g err=%v attempts=%d", value, err, attempts.Load())
	}
}
