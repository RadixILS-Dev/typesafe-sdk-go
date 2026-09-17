package typesafe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// Answer is one of *NoulAnswer, *ChoiceAnswer, or *ScoreAnswer.
type Answer interface {
	json.Marshaler
	isAnswer()
}

type NoulAnswer struct {
	Noul float64 `json:"noul"`
}
type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}
type ScoreAnswer struct {
	Score         float64         `json:"score"`
	Confidence    float64         `json:"confidence"`
	Legend        map[int]any     `json:"legend"`
	Probabilities map[int]float64 `json:"probabilities"`
}

func (*NoulAnswer) isAnswer()   {}
func (*ChoiceAnswer) isAnswer() {}
func (*ScoreAnswer) isAnswer()  {}

func (a NoulAnswer) MarshalJSON() ([]byte, error) {
	type fields NoulAnswer
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"noul", fields(a)})
}
func (a ChoiceAnswer) MarshalJSON() ([]byte, error) {
	type fields ChoiceAnswer
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"choice", fields(a)})
}
func (a ScoreAnswer) MarshalJSON() ([]byte, error) {
	type fields ScoreAnswer
	return json.Marshal(struct {
		Type string `json:"type"`
		fields
	}{"score", fields(a)})
}

// Usage preserves the distinction between unreported counts (nil) and zero.
type Usage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
}

// ResponseMetadata is excluded from JSON serialization. RawBody preserves the
// original payload, including unknown fields/answer types. The network body has
// already been read and closed; RawHTTPResponse.Body is an in-memory copy.
// RequestID is empty if the server did not return one.
type ResponseMetadata struct {
	RequestID       string         `json:"-"`
	RawHTTPResponse *http.Response `json:"-"`
	RawBody         []byte         `json:"-"`
}

// SystemOneResponse contains all recognized answers and convenience views by
// type. Grouped entries point to the same answers as Answers. Treat the maps and
// answers as read-only when sharing a response between goroutines.
type SystemOneResponse struct {
	ResponseMetadata
	Model   string                   `json:"model"`
	Usage   Usage                    `json:"usage"`
	Answers map[string]Answer        `json:"answers"`
	Nouls   map[string]*NoulAnswer   `json:"-"`
	Choices map[string]*ChoiceAnswer `json:"-"`
	Scores  map[string]*ScoreAnswer  `json:"-"`
}

type ModelMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

type ListModelsResponse struct {
	ResponseMetadata
	Models []ModelMetadata `json:"models"`
}

type fieldError struct{ path string }

func (e *fieldError) Error() string {
	return fmt.Sprintf("typesafe: invalid response data at %q", e.path)
}

func object(data []byte, path string) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if json.Unmarshal(data, &result) != nil || result == nil {
		return nil, &fieldError{path}
	}
	return result, nil
}
func isNull(data []byte) bool { return bytes.Equal(bytes.TrimSpace(data), []byte("null")) }
func required[T any](obj map[string]json.RawMessage, name, prefix string) (T, error) {
	var result T
	raw, ok := obj[name]
	path := name
	if prefix != "" {
		path = prefix + "." + name
	}
	if !ok || isNull(raw) || json.Unmarshal(raw, &result) != nil {
		return result, &fieldError{path}
	}
	return result, nil
}

func floatMap(obj map[string]json.RawMessage, name, prefix string) (map[string]float64, error) {
	raw, err := required[map[string]json.RawMessage](obj, name, prefix)
	if err != nil {
		return nil, err
	}
	result := make(map[string]float64, len(raw))
	for key := range raw {
		value, err := required[float64](raw, key, prefix+"."+name)
		if err != nil {
			return nil, err
		}
		result[key] = value
	}
	return result, nil
}

func parseAnswer(raw []byte, path string) (Answer, error) {
	obj, err := object(raw, path+".type")
	if err != nil {
		return nil, err
	}
	tag, err := required[string](obj, "type", path)
	if err != nil {
		return nil, err
	}
	switch tag {
	case "noul":
		value, err := required[float64](obj, "noul", path)
		if err != nil {
			return nil, err
		}
		return &NoulAnswer{Noul: value}, nil
	case "choice":
		choice, err := required[string](obj, "choice", path)
		if err != nil {
			return nil, err
		}
		confidence, err := required[float64](obj, "confidence", path)
		if err != nil {
			return nil, err
		}
		probabilities, err := floatMap(obj, "probabilities", path)
		if err != nil {
			return nil, err
		}
		return &ChoiceAnswer{Choice: choice, Confidence: confidence, Probabilities: probabilities}, nil
	case "score":
		score, err := required[float64](obj, "score", path)
		if err != nil {
			return nil, err
		}
		confidence, err := required[float64](obj, "confidence", path)
		if err != nil {
			return nil, err
		}
		legendRaw, err := required[map[string]json.RawMessage](obj, "legend", path)
		if err != nil {
			return nil, err
		}
		legend := make(map[int]any, len(legendRaw))
		for key, raw := range legendRaw {
			level, err := strconv.Atoi(key)
			if err != nil {
				return nil, &fieldError{path + ".legend"}
			}
			var description any
			if json.Unmarshal(raw, &description) != nil {
				return nil, &fieldError{path + ".legend." + key}
			}
			switch description.(type) {
			case string, map[string]any, []any:
				legend[level] = description
			default:
				return nil, &fieldError{path + ".legend." + key}
			}
		}
		probs, err := floatMap(obj, "probabilities", path)
		if err != nil {
			return nil, err
		}
		probabilities := make(map[int]float64, len(probs))
		for key, value := range probs {
			level, err := strconv.Atoi(key)
			if err != nil {
				return nil, &fieldError{path + ".probabilities"}
			}
			probabilities[level] = value
		}
		return &ScoreAnswer{Score: score, Confidence: confidence, Legend: legend, Probabilities: probabilities}, nil
	default:
		// Forward compatibility: retain unknown answers only in the raw body.
		return nil, nil
	}
}

// UnmarshalJSON validates required fields and builds the grouped answer views.
// Unknown fields and unknown answer types are ignored.
func (r *SystemOneResponse) UnmarshalJSON(data []byte) error {
	obj, err := object(data, "")
	if err != nil {
		return err
	}
	model, err := required[string](obj, "model", "")
	if err != nil {
		return err
	}
	usageRaw, err := required[map[string]json.RawMessage](obj, "usage", "")
	if err != nil {
		return err
	}
	var usage Usage
	for _, field := range []struct {
		name   string
		target **int64
	}{
		{"input_tokens", &usage.InputTokens}, {"output_tokens", &usage.OutputTokens},
	} {
		if raw, exists := usageRaw[field.name]; exists && !isNull(raw) {
			value, err := required[int64](usageRaw, field.name, "usage")
			if err != nil {
				return err
			}
			*field.target = &value
		}
	}
	answers, err := required[map[string]json.RawMessage](obj, "answers", "")
	if err != nil {
		return err
	}
	result := SystemOneResponse{Model: model, Usage: usage,
		Answers: make(map[string]Answer), Nouls: make(map[string]*NoulAnswer),
		Choices: make(map[string]*ChoiceAnswer), Scores: make(map[string]*ScoreAnswer)}
	for name, raw := range answers {
		answer, err := parseAnswer(raw, "answers."+name)
		if err != nil {
			return err
		}
		if answer == nil {
			continue
		}
		result.Answers[name] = answer
		switch answer := answer.(type) {
		case *NoulAnswer:
			result.Nouls[name] = answer
		case *ChoiceAnswer:
			result.Choices[name] = answer
		case *ScoreAnswer:
			result.Scores[name] = answer
		}
	}
	*r = result
	return nil
}

func (r *ListModelsResponse) UnmarshalJSON(data []byte) error {
	obj, err := object(data, "")
	if err != nil {
		return err
	}
	models, err := required[[]json.RawMessage](obj, "models", "")
	if err != nil {
		return err
	}
	result := make([]ModelMetadata, 0, len(models))
	for index, raw := range models {
		path := fmt.Sprintf("models[%d]", index)
		obj, err := object(raw, path)
		if err != nil {
			return err
		}
		name, err := required[string](obj, "name", path)
		if err != nil {
			return err
		}
		description, err := required[string](obj, "description", path)
		if err != nil {
			return err
		}
		releaseDate, err := required[string](obj, "release_date", path)
		if err != nil {
			return err
		}
		result = append(result, ModelMetadata{Name: name, Description: description, ReleaseDate: releaseDate})
	}
	*r = ListModelsResponse{Models: result}
	return nil
}
