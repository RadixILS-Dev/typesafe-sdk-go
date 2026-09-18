package typesafe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

type NoulAnswer struct{ Noul float64 }
type ChoiceAnswer struct {
	Choice        string
	Confidence    float64
	Probabilities map[string]float64
}
type ScoreAnswer struct {
	Score         float64
	Confidence    float64
	Legend        map[int]any
	Probabilities map[int]float64
}

// Usage distinguishes unreported token counts (nil) from zero.
type Usage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
}

// SystemOneResponse groups recognized answers by type. RawAnswers contains every
// answer, including unknown types and extra fields, without silently dropping data.
type SystemOneResponse struct {
	Model      string
	Usage      Usage
	RequestID  string
	RawAnswers map[string]json.RawMessage
	Nouls      map[string]NoulAnswer
	Choices    map[string]ChoiceAnswer
	Scores     map[string]ScoreAnswer
}

type ModelMetadata struct {
	Name        string
	Description string
	ReleaseDate string
}
type ListModelsResponse struct {
	RequestID string
	Models    []ModelMetadata
}

func decodeSystemOne(data []byte) (*SystemOneResponse, error) {
	obj, err := object(data, "")
	if err != nil {
		return nil, err
	}
	model, err := required[string](obj, "model", "")
	if err != nil {
		return nil, err
	}
	usage, err := required[Usage](obj, "usage", "")
	if err != nil {
		return nil, err
	}
	answers, err := required[map[string]json.RawMessage](obj, "answers", "")
	if err != nil {
		return nil, err
	}
	result := &SystemOneResponse{Model: model, Usage: usage, RawAnswers: answers,
		Nouls: make(map[string]NoulAnswer), Choices: make(map[string]ChoiceAnswer), Scores: make(map[string]ScoreAnswer)}
	for name, raw := range answers {
		if err := result.decodeAnswer(name, raw); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *SystemOneResponse) decodeAnswer(name string, raw []byte) error {
	path := "answers." + name
	obj, err := object(raw, path+".type")
	if err != nil {
		return err
	}
	tag, err := required[string](obj, "type", path)
	if err != nil {
		return err
	}
	switch tag {
	case "noul":
		value, err := required[float64](obj, "noul", path)
		if err != nil {
			return err
		}
		r.Nouls[name] = NoulAnswer{Noul: value}
	case "choice":
		answer, err := decodeChoice(obj, path)
		if err != nil {
			return err
		}
		r.Choices[name] = answer
	case "score":
		answer, err := decodeScore(obj, path)
		if err != nil {
			return err
		}
		r.Scores[name] = answer
	}
	// Unknown types remain visible in RawAnswers, not in a misleading typed value.
	return nil
}

func decodeChoice(obj map[string]json.RawMessage, path string) (ChoiceAnswer, error) {
	choice, err := required[string](obj, "choice", path)
	if err != nil {
		return ChoiceAnswer{}, err
	}
	confidence, err := required[float64](obj, "confidence", path)
	if err != nil {
		return ChoiceAnswer{}, err
	}
	probabilities, err := decodeProbabilities[string](obj, path)
	if err != nil {
		return ChoiceAnswer{}, err
	}
	return ChoiceAnswer{Choice: choice, Confidence: confidence, Probabilities: probabilities}, nil
}

func decodeScore(obj map[string]json.RawMessage, path string) (ScoreAnswer, error) {
	score, err := required[float64](obj, "score", path)
	if err != nil {
		return ScoreAnswer{}, err
	}
	confidence, err := required[float64](obj, "confidence", path)
	if err != nil {
		return ScoreAnswer{}, err
	}
	legend, err := required[map[int]any](obj, "legend", path)
	if err != nil {
		return ScoreAnswer{}, err
	}
	for level, description := range legend {
		switch description.(type) {
		case string, map[string]any, []any:
		default:
			return ScoreAnswer{}, &ResponseError{FieldPath: fmt.Sprintf("%s.legend.%d", path, level)}
		}
	}
	probabilities, err := decodeProbabilities[int](obj, path)
	if err != nil {
		return ScoreAnswer{}, err
	}
	return ScoreAnswer{Score: score, Confidence: confidence, Legend: legend, Probabilities: probabilities}, nil
}

func decodeProbabilities[K comparable](obj map[string]json.RawMessage, path string) (map[K]float64, error) {
	raw, err := required[map[K]json.RawMessage](obj, "probabilities", path)
	if err != nil {
		return nil, err
	}
	result := make(map[K]float64, len(raw))
	for key, data := range raw {
		var value float64
		if err := json.Unmarshal(data, &value); err != nil || isNull(data) {
			return nil, &ResponseError{FieldPath: fmt.Sprintf("%s.probabilities.%v", path, key), Err: err}
		}
		result[key] = value
	}
	return result, nil
}

func decodeModels(data []byte) (*ListModelsResponse, error) {
	obj, err := object(data, "")
	if err != nil {
		return nil, err
	}
	models, err := required[[]json.RawMessage](obj, "models", "")
	if err != nil {
		return nil, err
	}
	result := &ListModelsResponse{Models: make([]ModelMetadata, 0, len(models))}
	for index, raw := range models {
		model, err := decodeModel(raw, fmt.Sprintf("models[%d]", index))
		if err != nil {
			return nil, err
		}
		result.Models = append(result.Models, model)
	}
	return result, nil
}

func decodeModel(data []byte, path string) (ModelMetadata, error) {
	obj, err := object(data, path)
	if err != nil {
		return ModelMetadata{}, err
	}
	name, err := required[string](obj, "name", path)
	if err != nil {
		return ModelMetadata{}, err
	}
	description, err := required[string](obj, "description", path)
	if err != nil {
		return ModelMetadata{}, err
	}
	releaseDate, err := required[string](obj, "release_date", path)
	if err != nil {
		return ModelMetadata{}, err
	}
	return ModelMetadata{Name: name, Description: description, ReleaseDate: releaseDate}, nil
}

func object(data []byte, path string) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		return nil, &ResponseError{FieldPath: path, Err: err}
	}
	return result, nil
}

func isNull(data []byte) bool { return bytes.Equal(bytes.TrimSpace(data), []byte("null")) }

// Required fields need explicit presence checks: encoding/json otherwise turns
// missing/null numbers into zero, which is a valid but incorrect model answer.
func required[T any](obj map[string]json.RawMessage, name, prefix string) (T, error) {
	var result T
	path := name
	if prefix != "" {
		path = prefix + "." + name
	}
	raw, exists := obj[name]
	if !exists || isNull(raw) {
		return result, &ResponseError{FieldPath: path}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &typeError) && typeError.Field != "" {
			path += "." + typeError.Field
		}
		return result, &ResponseError{FieldPath: path, Err: err}
	}
	return result, nil
}
