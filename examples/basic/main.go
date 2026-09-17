package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/rocktavious/typesafe-sdk-go"
)

func main() {
	// Reads TYPESAFE_API_KEY from the environment by default.
	client, err := typesafe.NewClient()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	state := "I was charged twice. Please help ASAP."

	questions := typesafe.Questions{
		"billing": typesafe.Noul{
			Instructions: "Is this about billing?",
		},
		"tone": typesafe.Choice{
			Instructions: "What is the tone?",
			Criteria: map[string]string{
				"calm":  "",
				"angry": "",
			},
		},
		"urgency": typesafe.Score{
			Instructions: "How urgent is this?",
			Criteria:     []string{"low", "medium", "high"},
		},
	}

	result, err := client.SystemOne(ctx, state, questions)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(
		result.Nouls["billing"].Noul,
		result.Choices["tone"].Choice,
		result.Scores["urgency"].Score,
	)
}
