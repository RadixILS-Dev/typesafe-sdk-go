package typesafe

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Questions contains questions keyed by the names used in the response.
type Questions map[string]Question

// Question is a Noul, Choice, Score, or RawQuestion. Both values and pointers to
// the typed questions are accepted.
type Question interface {
	json.Marshaler
	isQuestion()
}

// NoulCriteria describes the "true" and "false" outcomes. Values may be strings,
// JSON objects, arrays, or nil. Extra keys are passed through to the API.
type NoulCriteria map[string]any

// Noul asks a yes/no question. Instructions may be a string, JSON object, or
// array. Nil optional fields are omitted; explicit empty strings are preserved.
type Noul struct {
	Instructions any `json:"instructions,omitempty"`
	Criteria     any `json:"criteria,omitempty"`
}

// Choice selects a named option. Criteria accepts map[string]string for simple
// descriptions or map[string]any for structured descriptions and nil values.
// Empty strings are sent as empty strings, not converted to JSON null.
type Choice struct {
	Instructions any `json:"instructions,omitempty"`
	Criteria     any `json:"criteria"`
}

// Score evaluates an ordered rubric, with levels starting at zero. Criteria
// accepts []string or a slice/array of JSON descriptions (for example []any).
// At least one level is required, matching the Python SDK's local validation.
type Score struct {
	Instructions any `json:"instructions,omitempty"`
	Criteria     any `json:"criteria"`
}

// RawQuestion passes through an extensible JSON question, including future
// question types. A nonempty string "type" is required. Choice and score
// questions also require a "criteria" key.
type RawQuestion map[string]any

func (Noul) isQuestion()        {}
func (Choice) isQuestion()      {}
func (Score) isQuestion()       {}
func (RawQuestion) isQuestion() {}

func (q Noul) MarshalJSON() ([]byte, error) {
	type fields Noul
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"noul", fields(q)})
}

func (q Choice) MarshalJSON() ([]byte, error) {
	type fields Choice
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"choice", fields(q)})
}

func (q Score) MarshalJSON() ([]byte, error) {
	type fields Score
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"score", fields(q)})
}

func (q RawQuestion) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(q))
}

func validateQuestions(questions Questions) error {
	if len(questions) == 0 {
		return fmt.Errorf("typesafe: at least one question is required")
	}
	for name, q := range questions {
		if q == nil || (reflect.ValueOf(q).Kind() == reflect.Pointer && reflect.ValueOf(q).IsNil()) {
			return fmt.Errorf("typesafe: question %q must not be nil", name)
		}
		switch q := q.(type) {
		case Score:
			if err := validateScore(name, q.Criteria); err != nil {
				return err
			}
		case *Score:
			if err := validateScore(name, q.Criteria); err != nil {
				return err
			}
		case RawQuestion:
			if err := validateRaw(name, q); err != nil {
				return err
			}
		case *RawQuestion:
			if err := validateRaw(name, *q); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRaw(name string, q RawQuestion) error {
	tag, ok := q["type"].(string)
	if !ok || tag == "" {
		return fmt.Errorf("typesafe: question %q requires a nonempty string type", name)
	}
	criteria, exists := q["criteria"]
	if (tag == "choice" || tag == "score") && !exists {
		return fmt.Errorf("typesafe: question %q requires criteria", name)
	}
	if tag == "score" {
		return validateScore(name, criteria)
	}
	return nil
}

func validateScore(name string, criteria any) error {
	v := reflect.ValueOf(criteria)
	if !v.IsValid() || ((v.Kind() == reflect.Slice || v.Kind() == reflect.Array || v.Kind() == reflect.Map || v.Kind() == reflect.String) && v.Len() == 0) {
		return fmt.Errorf("typesafe: score question %q has no criteria; at least one score is required", name)
	}
	// As in the Python client, detailed schema validation belongs to the API.
	return nil
}
