// This example finds candidate strings in code, then asks TypeSafe which role
// each candidate plays. Run with TYPESAFE_API_KEY set: go run ./examples/extraction
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

// These deliberately broad patterns find candidates; they do not validate them.
var (
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
)

const emailDocument = `From: Dana Whit <dana.whit@acme-corp.com>
To: billing@acme-corp.com
Cc: orders@acme-corp.com
Reply-To: dana.personal@gmail.com

Hi team - please don't use the billing alias for this one. Send my receipt to my
personal address instead. Thanks, Dana.`

// findCandidates trims and deduplicates matches, preserving document order and
// letter case. The same finder works with emailPattern, phonePattern, or moneyPattern.
func findCandidates(pattern *regexp.Regexp, text string) []string {
	seen := make(map[string]bool)
	var candidates []string
	for _, match := range pattern.FindAllString(text, -1) {
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

	emails := findCandidates(emailPattern, emailDocument)
	fmt.Println("candidates :", emails)

	receipt, err := client.Pick(ctx, emailDocument, "Which email address does the sender want their receipt sent to?", emails)
	if err != nil {
		log.Fatal(err)
	}
	sender, err := client.Pick(ctx, emailDocument, "Which email address did this message come from (the From line)?", emails)
	if err != nil {
		log.Fatal(err)
	}

	printEmailChoice("receipt", receipt)
	printEmailChoice("sender", sender)
}

func printEmailChoice(role string, answer typesafe.ChoiceAnswer) {
	if answer.Choice == typesafe.NoneChoice {
		fmt.Printf("%-7s -> : no matching address (conf %.2f)\n", role, answer.Confidence)
		return
	}
	// Pick returns the candidate verbatim. Lowercasing is an application-specific
	// display choice here, not SDK behavior or a universal email normalization rule.
	fmt.Printf("%-7s -> : %-28s (conf %.2f)\n", role, strings.ToLower(answer.Choice), answer.Confidence)
}
