package typesafe

import (
	"encoding/json"
	"testing"
)

func TestQuestionSerialization(t *testing.T) {
	cases := []struct {
		name     string
		question Question
		want     string
	}{
		{"default noul", Noul{}, `{"type":"noul"}`},
		{"empty instruction", Noul{Instructions: ""}, `{"type":"noul","instructions":""}`},
		{"empty criteria", Noul{Criteria: NoulCriteria{}}, `{"type":"noul","criteria":{}}`},
		{"noul null description", Noul{Instructions: []any{}, Criteria: NoulCriteria{"true": nil}}, `{"type":"noul","instructions":[],"criteria":{"true":null}}`},
		{"rich choice", Choice{Instructions: map[string]any{"question": "Team?"}, Criteria: map[string]any{"billing": map[string]any{"examples": []string{"charged twice"}}, "other": nil}}, `{"type":"choice","instructions":{"question":"Team?"},"criteria":{"billing":{"examples":["charged twice"]},"other":null}}`},
		{"single score", Score{Criteria: []string{"good"}}, `{"type":"score","criteria":["good"]}`},
		{"rich score", &Score{Criteria: []any{map[string]any{"summary": "bad"}, "good"}}, `{"type":"score","criteria":[{"summary":"bad"},"good"]}`},
		{"raw future", RawQuestion{"type": "future", "nested": map[string]any{"k": nil}}, `{"type":"future","nested":{"k":null}}`},
		{"raw null preserved", RawQuestion{"type": "noul", "instructions": nil}, `{"type":"noul","instructions":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateQuestions(Questions{"q": tc.question}); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(tc.question)
			if err != nil {
				t.Fatal(err)
			}
			jsonEqual(t, data, tc.want)
		})
	}
}
