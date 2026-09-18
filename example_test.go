package typesafe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/RadixILS-Dev/typesafe-sdk-go"
)

// stubTransport returns canned JSON so these examples run offline and print
// deterministic output. Real programs can omit WithHTTPClient and use the
// SDK's default client, or supply their own for timeouts and instrumentation.
type stubTransport struct {
	status  int
	body    string
	headers http.Header
}

func (s stubTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	headers := s.headers.Clone()
	if headers == nil {
		headers = http.Header{}
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     headers,
		Body:       io.NopCloser(bytes.NewBufferString(s.body)),
		Request:    request,
	}, nil
}

func stubClient(status int, headers http.Header, body string) *http.Client {
	return &http.Client{Transport: stubTransport{status: status, body: body, headers: headers}}
}

const systemOneBody = `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{
	"billing":{"type":"noul","noul":0.98},
	"tone":{"type":"choice","choice":"angry","confidence":0.9,"probabilities":{"calm":0.1,"angry":0.9}},
	"urgency":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"low","1":"medium","2":"high"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}}}`

func ExampleNewClient() {
	client, err := typesafe.NewClient(typesafe.WithAPIKey("your-api-key"))
	if err != nil {
		log.Fatal(err)
	}
	// Configuration is fixed at construction; the client is safe to share.
	fmt.Println("client ready:", client != nil)
	// Output: client ready: true
}

func ExampleClient_SystemOne() {
	client, err := typesafe.NewClient(
		typesafe.WithAPIKey("your-api-key"),
		typesafe.WithHTTPClient(stubClient(http.StatusOK, nil, systemOneBody)),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.SystemOne(context.Background(), "I was charged twice. Please help ASAP.", typesafe.Questions{
		"billing": typesafe.Noul{Instructions: "Is this about billing?"},
		"tone": typesafe.Choice{
			Instructions: "What is the tone?",
			Criteria:     map[string]string{"calm": "", "angry": ""},
		},
		"urgency": typesafe.Score{
			Instructions: "How urgent is this?",
			Criteria:     []string{"low", "medium", "high"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("billing  %.2f\n", result.Nouls["billing"].Noul)
	fmt.Printf("tone     %s\n", result.Choices["tone"].Choice)
	fmt.Printf("urgency  %.1f\n", result.Scores["urgency"].Score)
	fmt.Printf("tokens   %d in / %d out\n", *result.Usage.InputTokens, *result.Usage.OutputTokens)
	// Output:
	// billing  0.98
	// tone     angry
	// urgency  1.7
	// tokens   12 in / 3 out
}

func ExampleClient_Pick() {
	client, err := typesafe.NewClient(
		typesafe.WithAPIKey("your-api-key"),
		typesafe.WithHTTPClient(stubClient(http.StatusOK, nil,
			`{"model":"jev-latest","usage":{},"answers":{"q":{"type":"choice","choice":"USD 42.00","confidence":0.91,"probabilities":{"USD 42.00":0.91,"USD 21.00":0.09,"none":0.00}}}}`)),
	)
	if err != nil {
		log.Fatal(err)
	}

	answer, err := client.Pick(context.Background(), "Invoice total: USD 42.00",
		"Which amount is the invoice total?", []string{"USD 42.00", "USD 21.00"})
	if err != nil {
		log.Fatal(err)
	}
	if answer.Choice == typesafe.NoneChoice {
		fmt.Println("no candidate matched")
		return
	}
	// The winning candidate is returned verbatim, without trimming or rewriting.
	fmt.Printf("%s (%.2f)\n", answer.Choice, answer.Confidence)
	// Output: USD 42.00 (0.91)
}

func ExampleClient_IsTrue() {
	client, err := typesafe.NewClient(
		typesafe.WithAPIKey("your-api-key"),
		typesafe.WithHTTPClient(stubClient(http.StatusOK, nil,
			`{"model":"jev-latest","usage":{},"answers":{"q":{"type":"noul","noul":0.77}}}`)),
	)
	if err != nil {
		log.Fatal(err)
	}

	probability, err := client.IsTrue(context.Background(), "Invoice total: USD 42.00", "Does this state an invoice total?")
	if err != nil {
		log.Fatal(err)
	}
	// IsTrue returns a probability, not a thresholded boolean.
	fmt.Printf("%.2f\n", probability)
	// Output: 0.77
}

func ExampleClient_ListModels() {
	client, err := typesafe.NewClient(
		typesafe.WithAPIKey("your-api-key"),
		typesafe.WithHTTPClient(stubClient(http.StatusOK, nil,
			`{"models":[{"name":"jev-latest","description":"Fastest model","release_date":"2026-09-14"}]}`)),
	)
	if err != nil {
		log.Fatal(err)
	}

	models, err := client.ListModels(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, model := range models.Models {
		fmt.Printf("%s released %s\n", model.Name, model.ReleaseDate)
	}
	// Output: jev-latest released 2026-09-14
}

func ExampleAPIError() {
	responseHeaders := http.Header{}
	responseHeaders.Set("X-TypeSafe-Request-ID", "req-42")
	responseHeaders.Set("Retry-After", "2")

	client, err := typesafe.NewClient(
		typesafe.WithAPIKey("your-api-key"),
		typesafe.WithMaxAttempts(1), // Disable retries to inspect the first response.
		typesafe.WithHTTPClient(stubClient(http.StatusTooManyRequests, responseHeaders,
			`{"message":"Rate limit exceeded"}`)),
	)
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.ListModels(context.Background())
	var api *typesafe.APIError
	switch {
	case errors.As(err, &api) && api.StatusCode == http.StatusTooManyRequests:
		fmt.Printf("%d %s (request %s, retry after %ss)\n",
			api.StatusCode, api.Message, api.RequestID, api.Headers.Get("Retry-After"))
	case err != nil:
		log.Fatal(err)
	}
	// Output: 429 Rate limit exceeded (request req-42, retry after 2s)
}

// Questions are plain Go values: typed questions add their own "type" tag, and
// descriptions may be structured JSON. The API validates the result.
func ExampleQuestions() {
	questions := typesafe.Questions{
		"fraud": typesafe.Noul{
			Instructions: map[string]any{"question": "Is this refund suspicious?", "rules": []string{"Ignore tone."}},
			Criteria:     typesafe.NoulCriteria{"true": "Suspicious", "false": "Ordinary"},
		},
		"queue": typesafe.Choice{
			Instructions: "Which team should handle this?",
			Criteria:     map[string]any{"billing": nil, "chargeback": "Disputed card payment"},
		},
	}
	data, err := json.Marshal(questions)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(data))
	// Output: {"fraud":{"type":"noul","instructions":{"question":"Is this refund suspicious?","rules":["Ignore tone."]},"criteria":{"false":"Ordinary","true":"Suspicious"}},"queue":{"type":"choice","instructions":"Which team should handle this?","criteria":{"billing":null,"chargeback":"Disputed card payment"}}}
}
