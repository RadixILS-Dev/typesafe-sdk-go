package typesafe

import "encoding/json"

// Questions maps names to Noul, Choice, Score, or raw JSON question objects.
// Values are serialized unchanged; the API validates their shape.
type Questions map[string]any

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
// The API validates the rubric.
type Score struct {
	Instructions any `json:"instructions,omitempty"`
	Criteria     any `json:"criteria"`
}

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
