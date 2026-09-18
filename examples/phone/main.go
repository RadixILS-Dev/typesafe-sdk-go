// This example selects a phone number with Pick and an office country with
// Classify. Run with TYPESAFE_API_KEY set: go run ./examples/phone
package main

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/RadixILS-Dev/typesafe-sdk-go"
)

// This broad pattern finds candidates; it does not validate phone numbers.
var phonePattern = regexp.MustCompile(`\(?\+?\d[\d\s()\-.]{6,}\d`)

const phoneDocument = `Reach our San Francisco office at these numbers: main desk (415) 555-0199,
billing fax (415) 555-0142, and my direct cell (415) 555-0177. Call the cell if it's urgent.`

func findPhoneCandidates(text string) []string {
	seen := make(map[string]bool)
	var candidates []string
	for _, match := range phonePattern.FindAllString(text, -1) {
		span := strings.TrimSpace(match)
		if span != "" && !seen[span] {
			seen[span] = true
			candidates = append(candidates, span)
		}
	}
	return candidates
}

func main() {
	client, err := typesafe.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	phones := findPhoneCandidates(phoneDocument)
	fmt.Println("candidates :", phones)

	mobile, err := client.Pick(ctx, phoneDocument,
		"Which of these is the direct mobile / cell number?", phones)
	if err != nil {
		log.Fatal(err)
	}
	region, err := client.Classify(ctx, phoneDocument,
		"In what country is this office located?",
		[]string{"US", "GB", "DE", "FR", "CA", "AU"})
	if err != nil {
		log.Fatal(err)
	}

	if mobile.Choice == typesafe.NoneChoice {
		fmt.Printf("mobile  -> : no matching number (conf %.2f)\n", mobile.Confidence)
	} else {
		// Keep the candidate verbatim; country classification does not normalize it.
		fmt.Printf("mobile  -> : %s (conf %.2f)\n", mobile.Choice, mobile.Confidence)
	}
	fmt.Printf("country -> : %s (conf %.2f)\n", region.Choice, region.Confidence)
}
