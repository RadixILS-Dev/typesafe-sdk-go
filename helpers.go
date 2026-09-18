package typesafe

import (
	"context"
	"fmt"
)

// NoneChoice is Pick's fallback when none of the candidate spans fits.
const NoneChoice = "none"

// Pick selects a candidate span verbatim, or NoneChoice if none fits. Candidates
// must not include the reserved NoneChoice label. It does not find spans in state;
// the caller supplies them. State and instructions may be text or structured JSON.
func (c *Client) Pick(ctx context.Context, state any, instructions any, candidates []string) (ChoiceAnswer, error) {
	criteria := make(map[string]any, len(candidates)+1)
	for _, candidate := range candidates {
		if candidate == NoneChoice {
			return ChoiceAnswer{}, fmt.Errorf("typesafe: Pick candidate %q is reserved for the no-match option", NoneChoice)
		}
		criteria[candidate] = nil
	}
	criteria[NoneChoice] = "None of these is the requested value."
	return c.choose(ctx, state, instructions, criteria)
}

// Classify selects one of the supplied labels without adding a no-match option.
// State and instructions may be text or structured JSON.
func (c *Client) Classify(ctx context.Context, state any, instructions any, options []string) (ChoiceAnswer, error) {
	criteria := make(map[string]any, len(options))
	for _, option := range options {
		criteria[option] = nil
	}
	return c.choose(ctx, state, instructions, criteria)
}

// IsTrue returns the probability of yes, not a thresholded boolean. State and
// instructions may be text or structured JSON.
func (c *Client) IsTrue(ctx context.Context, state any, instructions any) (float64, error) {
	result, err := c.SystemOne(ctx, state, Questions{"q": Noul{Instructions: instructions}})
	if err != nil {
		return 0, err
	}
	answer, ok := result.Nouls["q"]
	if !ok {
		return 0, &ResponseError{FieldPath: "answers.q", RequestID: result.RequestID, Err: fmt.Errorf("expected a noul answer")}
	}
	return answer.Noul, nil
}

func (c *Client) choose(ctx context.Context, state any, instructions any, criteria map[string]any) (ChoiceAnswer, error) {
	result, err := c.SystemOne(ctx, state, Questions{"q": Choice{Instructions: instructions, Criteria: criteria}})
	if err != nil {
		return ChoiceAnswer{}, err
	}
	answer, ok := result.Choices["q"]
	if !ok {
		return ChoiceAnswer{}, &ResponseError{FieldPath: "answers.q", RequestID: result.RequestID, Err: fmt.Errorf("expected a choice answer")}
	}
	if _, ok := criteria[answer.Choice]; !ok {
		return ChoiceAnswer{}, &ResponseError{FieldPath: "answers.q.choice", RequestID: result.RequestID, Err: fmt.Errorf("selected value is not one of the supplied options")}
	}
	return answer, nil
}
