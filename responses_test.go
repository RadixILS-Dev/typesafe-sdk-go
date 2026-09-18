package typesafe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
)

func TestResponseValidation(t *testing.T) {
	cases := []struct{ body, path string }{
		{`not json`, ""}, {`null`, ""}, {`[]`, ""}, {`{}`, "model"},
		{`{"model":null,"usage":{},"answers":{}}`, "model"},
		{`{"model":"m","answers":{}}`, "usage"},
		{`{"model":"m","usage":{}}`, "answers"},
		{`{"model":"m","usage":{},"answers":null}`, "answers"},
		{`{"model":"m","usage":{"input_tokens":0.5},"answers":{}}`, "usage.input_tokens"},
		{`{"model":"m","usage":{},"answers":{"q":null}}`, "answers.q.type"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":7}}}`, "answers.q.type"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"noul"}}}`, "answers.q.noul"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"noul","noul":null}}}`, "answers.q.noul"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"noul","noul":"0.5"}}}`, "answers.q.noul"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"choice","choice":"a","probabilities":{}}}}`, "answers.q.confidence"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":null}}}}`, "answers.q.probabilities.a"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,"legend":{"x":"bad"},"probabilities":{}}}}`, "answers.q.legend.x"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,"legend":{"0":null},"probabilities":{}}}}`, "answers.q.legend.0"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,"legend":{},"probabilities":{"x":1}}}}`, "answers.q.probabilities.x"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,"legend":{"1":true},"probabilities":{}}}}`, "answers.q.legend.1"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,"legend":{},"probabilities":{"0":null}}}}`, "answers.q.probabilities.0"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,"legend":{},"probabilities":{"99999999999999999999":1}}}}`, "answers.q.probabilities.99999999999999999999"},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":[]}}}}`, "answers.q.probabilities.a"},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			var attempts atomic.Int32
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.Header().Set("X-TypeSafe-Request-ID", "req-invalid")
				fmt.Fprint(w, tc.body)
			})
			_, err := client.SystemOne(context.Background(), "x", Questions{"q": Noul{}})
			var validation *ResponseError
			if !errors.As(err, &validation) {
				t.Fatalf("got %T: %v", err, err)
			}
			if validation.FieldPath != tc.path || validation.RequestID != "req-invalid" {
				t.Fatalf("wrong validation error: %+v", validation)
			}
			if attempts.Load() != 1 {
				t.Error("schema error retried")
			}
		})
	}
}

// encoding/json accepts these spellings for integer map keys, so the SDK keeps
// accepting them; the collisions they create are rejected below.
func TestScoreIntegerMapKeys(t *testing.T) {
	body := `{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,` +
		`"legend":{"0":"low","+1":"high"},"probabilities":{"0":0.5,"+1":0.5}}}}`
	result, err := decodeSystemOne([]byte(body))
	if err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	score := result.Scores["q"]
	if score.Legend[1] != "high" || score.Probabilities[0] != 0.5 || score.Probabilities[1] != 0.5 {
		t.Fatalf("integer keys lost: %+v", score)
	}
}

func TestScoreCollidingMapKeys(t *testing.T) {
	// "01" and "1" name the same level, and which one survives decoding depends on
	// map iteration order, so both are reported instead of one being kept.
	for _, tc := range []struct {
		body  string
		paths []string
	}{
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,` +
			`"legend":{"01":"low","1":"high"},"probabilities":{}}}}`,
			[]string{"answers.q.legend.01", "answers.q.legend.1"}},
		{`{"model":"m","usage":{},"answers":{"q":{"type":"score","score":0,"confidence":1,` +
			`"legend":{},"probabilities":{"01":0.5,"1":0.5}}}}`,
			[]string{"answers.q.probabilities.01", "answers.q.probabilities.1"}},
	} {
		_, err := decodeSystemOne([]byte(tc.body))
		var invalid *ResponseError
		if !errors.As(err, &invalid) || !slices.Contains(tc.paths, invalid.FieldPath) {
			t.Fatalf("%s: unexpected error %v", tc.body, err)
		}
	}
}

func TestForwardCompatibleResponse(t *testing.T) {
	body := `{"model":"m","extra":true,"usage":{"output_tokens":0,"billing_units":99},"answers":{
	 "known":{"type":"noul","noul":0,"future":true},"unknown":{"type":"future","value":7},
	 "score":{"type":"score","score":0,"confidence":1,"legend":{"0":{"examples":["a",{"note":null}]}},"probabilities":{"0":1}}
	}}`
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
	result, err := client.SystemOne(context.Background(), "x", Questions{"q": Noul{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RawAnswers) != 3 || len(result.Nouls) != 1 || len(result.Choices) != 0 || len(result.Scores) != 1 || result.Nouls["known"].Noul != 0 {
		t.Fatal("unknown answer handling failed")
	}
	if result.Usage.InputTokens != nil || result.Usage.OutputTokens == nil || *result.Usage.OutputTokens != 0 {
		t.Fatal("missing and zero token counts conflated")
	}
	if result.RequestID != "" {
		t.Fatal("incorrect metadata")
	}
	jsonEqual(t, result.RawAnswers["unknown"], `{"type":"future","value":7}`)
	jsonEqual(t, result.RawAnswers["known"], `{"type":"noul","noul":0,"future":true}`)
	legend := result.Scores["score"].Legend[0].(map[string]any)
	if len(legend["examples"].([]any)) != 2 {
		t.Fatal("nested JSON lost")
	}
}

func TestModelResponseValidation(t *testing.T) {
	for _, tc := range []struct{ body, path string }{
		{`{}`, "models"}, {`{"models":null}`, "models"}, {`{"models":"bad"}`, "models"},
		{`{"models":[{"name":"m","description":"d"}]}`, "models[0].release_date"},
	} {
		_, err := decodeModels([]byte(tc.body))
		var field *ResponseError
		if !errors.As(err, &field) || field.FieldPath != tc.path {
			t.Fatalf("%s: unexpected error %v", tc.body, err)
		}
	}
}

func TestErrorMessageExtraction(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":"one","message":"two"}`, "one"}, {`{"error":{"message":"nested"}}`, "nested"},
		{`{"message":"message"}`, "message"}, {`{"detail":"detail"}`, "detail"},
		{`{"detail":{"message":"nested detail"}}`, "nested detail"},
		{`"plain JSON string"`, "plain JSON string"}, {`plain text`, "plain text"},
		{`{"detail":[{"loc":["body","q",0],"msg":"bad"},{"msg":"other"}]}`, "q.0: bad; other"},
	} {
		if got := errorMessage(decodeErrorBody([]byte(tc.body))); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.body, got, tc.want)
		}
	}
}
