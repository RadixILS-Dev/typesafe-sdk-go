// This example adjudicates an auto-insurance claim: fourteen grounded yes/no
// questions go to SystemOne in a single call over one JSON claim file, and the
// answers are then cross-checked against arithmetic computed offline from that
// same file.
//
// The claim file is deliberately inconsistent. Automated triage approved the full
// $3,250, but the policy excludes rental reimbursement, applies a $500 deductible,
// and requires a police report above $2,000. The questions surface the judgement
// calls; the offline checks catch the arithmetic.
//
// A live run against jev-latest agreed with all seven offline checks and correctly
// called the documentation insufficient. What it did not settle was the exclusion:
// "track/competitive driving" is listed, and the narrative puts the car parked in
// the spectator lot, so both covered and exclusion came back at 0.53 - a coin flip,
// not a yes. Those low-margin answers, and the missing human sign-off, are what
// should route this claim to a supervisor rather than to payment.
//
// Run with TYPESAFE_API_KEY set: go run ./examples/claims
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/RadixILS-Dev/typesafe-sdk-go"
)

// yesThreshold turns a Noul probability into a yes/no for display and for the
// cross-check. Noul answers are probabilities of yes; any threshold is an
// application decision, not SDK behavior.
const yesThreshold = 0.5

// uncertainMargin marks answers close to yesThreshold as undecided. A 0.53 is not
// a weak "yes" here; it is the model declining to choose, and on this claim it is
// the strongest signal for a human review.
const uncertainMargin = 0.15

// lowMargin labels a probability for the report.
func lowMargin(probability float64) string {
	diff := probability - yesThreshold
	if diff < 0 {
		diff = -diff
	}
	if diff <= uncertainMargin {
		return "undecided"
	}
	return ""
}

const dateLayout = "2006-01-02"

// The claim types let the offline checks read the same file the model sees without
// stringly typed map lookups. The JSON tags are what SystemOne receives; the Go
// field names are not part of the wire format.
type (
	claimFile struct {
		Policy        policy         `json:"policy"`
		Claim         claim          `json:"claim"`
		AdjusterNotes []adjusterNote `json:"adjuster_notes"`
		ClaimHistory  map[string]int `json:"claim_history"`
	}
	policy struct {
		PolicyID            string    `json:"policy_id"`
		Policyholder        string    `json:"policyholder"`
		Effective           string    `json:"effective"`
		Expires             string    `json:"expires"`
		Coverages           coverages `json:"coverages"`
		Deductible          float64   `json:"deductible"`
		PerIncidentLimit    float64   `json:"per_incident_limit"`
		ListedDrivers       []string  `json:"listed_drivers"`
		Exclusions          []string  `json:"exclusions"`
		ReportingWindowDays int       `json:"reporting_window_days"`
		PoliceReportOver    float64   `json:"police_report_required_over"`
	}
	coverages struct {
		Collision           bool `json:"collision"`
		RentalReimbursement bool `json:"rental_reimbursement"`
	}
	claim struct {
		ClaimID       string     `json:"claim_id"`
		IncidentDate  string     `json:"incident_date"`
		ReportedDate  string     `json:"reported_date"`
		Driver        string     `json:"driver"`
		Description   string     `json:"description"`
		AmountClaimed float64    `json:"amount_claimed"`
		LineItems     []lineItem `json:"line_items"`
		Documentation []string   `json:"documentation"`
	}
	lineItem struct {
		Item string  `json:"item"`
		Cost float64 `json:"cost"`
	}
	adjusterNote struct {
		Author string `json:"author"`
		Note   string `json:"note"`
	}
)

// sampleClaim is the claim file under review. Dates are ISO 8601 so the offline
// checks can compare them without a locale-dependent parse.
func sampleClaim() claimFile {
	return claimFile{
		Policy: policy{
			PolicyID:            "AP-77413",
			Policyholder:        "Dana M.",
			Effective:           "2026-01-15",
			Expires:             "2027-01-15",
			Coverages:           coverages{Collision: true, RentalReimbursement: false},
			Deductible:          500.00,
			PerIncidentLimit:    10000.00,
			ListedDrivers:       []string{"Dana M.", "Sam M."},
			Exclusions:          []string{"track/competitive driving", "drivers not listed on the policy"},
			ReportingWindowDays: 10,
			PoliceReportOver:    2000.00,
		},
		Claim: claim{
			ClaimID:      "CLM-55029",
			IncidentDate: "2026-06-28",
			ReportedDate: "2026-07-04",
			Driver:       "Sam M.",
			Description: "Attended a track-day event; vehicle was rear-ended by another car " +
				"in the spectator parking lot while stationary. Not on the circuit.",
			AmountClaimed: 3250.00,
			LineItems: []lineItem{
				{Item: "rear bumper replacement", Cost: 1700.00},
				{Item: "paint + refinish", Cost: 800.00},
				{Item: "parking-sensor recalibration", Cost: 450.00},
				{Item: "rental car (6 days)", Cost: 300.00},
			},
			Documentation: []string{"repair estimate (PDF)", "8 damage photos"},
		},
		AdjusterNotes: []adjusterNote{{
			Author: "auto-triage",
			Note: "Collision coverage active. Approved. Pay full amount $3,250 to " +
				"policyholder, 5-10 business days.",
		}},
		ClaimHistory: map[string]int{"claims_last_12mo": 2, "prior_denied": 0},
	}
}

// questionOrder fixes the display order and groups related questions together.
// main_test.go keeps it in step with the question set.
var questionOrder = []string{
	"covered", "exclusion", "on_circuit", "rental_eligible",
	"within_window", "reported_timely", "within_limit", "line_items_sum",
	"docs_sufficient", "deductible", "subrogation",
	"fraud_flag", "human_review", "manual_review",
}

// questions are the adjudication questions, asked together so the model reads the
// file once and answers all of them against it. One call is cheaper and more
// self-consistent than fourteen separate ones, and each answer keeps its own
// probability.
//
// Most questions need only an instruction. Where "yes" could reasonably mean two
// things, NoulCriteria pins down the reading instead of leaving it to chance.
func questions() typesafe.Questions {
	return typesafe.Questions{
		"covered": typesafe.Noul{
			Instructions: "Is the loss covered under the policy's collision coverage?",
		},
		"exclusion": typesafe.Noul{
			// The exclusion list says "track/competitive driving" and the narrative
			// says the car was parked, so say what counts as a yes.
			Instructions: "Does a policy exclusion apply to this loss?",
			Criteria: typesafe.NoulCriteria{
				"true":  "A listed exclusion covers these facts as written, taking the incident description at face value.",
				"false": "No listed exclusion covers these facts as written.",
			},
		},
		"on_circuit": typesafe.Noul{
			Instructions: "Did the collision happen while the vehicle was being driven on the racetrack itself?",
		},
		"rental_eligible": typesafe.Noul{
			Instructions: "Is the rental-car cost eligible for reimbursement under this policy?",
		},
		"within_window": typesafe.Noul{
			Instructions: "Did the loss occur within the policy's active coverage period?",
		},
		"reported_timely": typesafe.Noul{
			Instructions: "Was the loss reported within the policy's required window?",
		},
		"within_limit": typesafe.Noul{
			Instructions: "Is the amount claimed within the per-incident coverage limit?",
		},
		"line_items_sum": typesafe.Noul{
			Instructions: "Do the claimed line-item costs add up to the total amount claimed?",
		},
		"docs_sufficient": typesafe.Noul{
			Instructions: "Is the attached documentation sufficient to adjudicate the claim as-is?",
			Criteria: typesafe.NoulCriteria{
				"true":  "Everything the policy requires for a loss of this size is present in the documentation list.",
				"false": "At least one document the policy requires for a loss of this size is missing.",
			},
		},
		"deductible": typesafe.Noul{
			Instructions: "Would the $500 deductible be correctly applied before any payout?",
			Criteria: typesafe.NoulCriteria{
				"true":  "A payout on this claim must be reduced by the $500 deductible.",
				"false": "The deductible does not reduce a payout on this claim.",
			},
		},
		"subrogation": typesafe.Noul{
			Instructions: "Is there a potentially at-fault third party the insurer could pursue for subrogation recovery?",
		},
		"fraud_flag": typesafe.Noul{
			Instructions: "Are there indicators that warrant a fraud review?",
		},
		"human_review": typesafe.Noul{
			// The only note in the file is automated, so this asks about the record,
			// not about what should happen next.
			Instructions: "Was payment approved by automated triage without a human adjuster's review?",
			Criteria: typesafe.NoulCriteria{
				"true":  "The adjuster record shows an approval and no human adjuster review.",
				"false": "A human adjuster reviewed or approved the claim.",
			},
		},
		"manual_review": typesafe.Noul{
			Instructions: "Should this claim be routed for manual/supervisor review before payout?",
			Criteria: typesafe.NoulCriteria{
				"true":  "Something in the file - a possible exclusion, missing required documentation, an ineligible line item, or a missing human review - makes immediate payout unsafe.",
				"false": "The file supports paying out without further human review.",
			},
		},
	}
}

// check is an offline answer to one of the questions, computed from the claim file
// itself. The model is asked the same question; agreement is reassuring and
// disagreement is worth a human's attention before payout.
type check struct {
	Question string // name of the question this cross-checks
	Answer   bool   // answer the file itself implies
	Detail   string // why, for the printed report
}

// autoTriageAuthors are note authors that are not human adjusters. A real system
// would carry an explicit reviewer identity or review flag on the approval.
var autoTriageAuthors = map[string]bool{"auto-triage": true}

// offlineChecks answers the questions that need no judgement: dates, amounts, the
// policy's own coverage flags, and who signed the approval. Each one names the
// question it cross-checks, so the report can line the two answers up.
func offlineChecks(file claimFile) []check {
	effective := parseDate(file.Policy.Effective)
	expires := parseDate(file.Policy.Expires)
	incident := parseDate(file.Claim.IncidentDate)
	reported := parseDate(file.Claim.ReportedDate)

	lineTotal := lineItemTotal(file.Claim)
	rentalTotal, rentalItems := ineligibleLines(file)
	reportedDays := int(reported.Sub(incident).Hours() / 24)
	needsPoliceReport := file.Claim.AmountClaimed > file.Policy.PoliceReportOver
	hasPoliceReport := hasDocument(file, "police")

	return []check{
		{"within_window", !incident.Before(effective) && !incident.After(expires),
			fmt.Sprintf("loss %s against policy period %s..%s", file.Claim.IncidentDate, file.Policy.Effective, file.Policy.Expires)},
		{"reported_timely", reportedDays >= 0 && reportedDays <= file.Policy.ReportingWindowDays,
			fmt.Sprintf("reported %d day(s) after the loss; window is %d", reportedDays, file.Policy.ReportingWindowDays)},
		{"within_limit", file.Claim.AmountClaimed <= file.Policy.PerIncidentLimit,
			fmt.Sprintf("%.2f claimed against a %.2f per-incident limit", file.Claim.AmountClaimed, file.Policy.PerIncidentLimit)},
		{"line_items_sum", closeTo(lineTotal, file.Claim.AmountClaimed),
			fmt.Sprintf("line items total %.2f against %.2f claimed", lineTotal, file.Claim.AmountClaimed)},
		{"docs_sufficient", !needsPoliceReport || hasPoliceReport,
			fmt.Sprintf("a police report is required above %.2f (claimed %.2f); documentation on file: %s",
				file.Policy.PoliceReportOver, file.Claim.AmountClaimed, joinedOrNone(file.Claim.Documentation))},
		{"rental_eligible", file.Policy.Coverages.RentalReimbursement && len(rentalItems) == 0,
			fmt.Sprintf("rental_reimbursement=%t; %.2f of rental lines claimed (%s)",
				file.Policy.Coverages.RentalReimbursement, rentalTotal, joinedOrNone(rentalItems))},
		{"human_review", approvedWithoutHuman(file), reviewDetail(file)},
	}
}

// reviewDetail explains the human_review check by naming who wrote the notes.
func reviewDetail(file claimFile) string {
	authors := make([]string, 0, len(file.AdjusterNotes))
	for _, note := range file.AdjusterNotes {
		authors = append(authors, note.Author)
	}
	return fmt.Sprintf("notes are authored by %s; an approval with no human author trips this check", joinedOrNone(authors))
}

// lineItemTotal adds the claimed line items. It is the arithmetic the
// "line_items_sum" question asks about.
func lineItemTotal(c claim) float64 {
	var total float64
	for _, item := range c.LineItems {
		total += item.Cost
	}
	return total
}

// ineligibleLines totals the line items the policy's coverage flags rule out.
// Matching rental lines on their item text is a stand-in for the coverage-code
// mapping a real system would use.
func ineligibleLines(file claimFile) (total float64, items []string) {
	for _, item := range file.Claim.LineItems {
		rental := strings.Contains(strings.ToLower(item.Item), "rental")
		if rental && !file.Policy.Coverages.RentalReimbursement {
			total += item.Cost
			items = append(items, item.Item)
		}
	}
	return total, items
}

// approvedWithoutHuman reports an approval with no human adjuster note anywhere in
// the record - the condition the "human_review" question asks about.
func approvedWithoutHuman(file claimFile) bool {
	approved, reviewedByHuman := false, false
	for _, note := range file.AdjusterNotes {
		if strings.Contains(strings.ToLower(note.Note), "approved") {
			approved = true
		}
		if !autoTriageAuthors[note.Author] {
			reviewedByHuman = true
		}
	}
	return approved && !reviewedByHuman
}

// payout is the arithmetic the automated triage note skipped: ineligible line
// items out first, then the deductible. It is computed from the claim file, not
// from a model answer.
type payout struct {
	Claimed    float64
	Ineligible float64
	Skipped    []string
	Deductible float64
	Net        float64
}

func calculatePayout(file claimFile) payout {
	ineligible, skipped := ineligibleLines(file)
	net := file.Claim.AmountClaimed - ineligible - file.Policy.Deductible
	if net < 0 {
		net = 0
	}
	return payout{
		Claimed:    file.Claim.AmountClaimed,
		Ineligible: ineligible,
		Skipped:    skipped,
		Deductible: file.Policy.Deductible,
		Net:        net,
	}
}

func hasDocument(file claimFile, keyword string) bool {
	for _, doc := range file.Claim.Documentation {
		if strings.Contains(strings.ToLower(doc), strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// parseDate reads an ISO 8601 date. The claim file is authored here, so a bad
// date is a bug in this program rather than untrusted input.
func parseDate(date string) time.Time {
	parsed, err := time.Parse(dateLayout, date)
	if err != nil {
		log.Fatalf("parse date %q: %v", date, err)
	}
	return parsed
}

func closeTo(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.005 // cent-level money
}

func yesNo(answer bool) string {
	if answer {
		return "yes"
	}
	return "no"
}

func verdict(probability float64) string { return yesNo(probability >= yesThreshold) }

// joinedOrNone keeps an empty list readable in the report without Go's bracket
// formatting for slices.
func joinedOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// orNA keeps a missing value out of the report rather than printing a blank.
func orNA(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// tokens renders an optional token count. Usage counts are *int64 so that a model
// that reports no count is distinguishable from one that reports zero.
func tokens(count *int64) string {
	if count == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *count)
}

// render prints the model's answers, the offline cross-checks, and the payout
// arithmetic. It writes to w and takes the response as an argument so that
// main_test.go can drive it with a canned response instead of a live call.
func render(w io.Writer, file claimFile, result *typesafe.SystemOneResponse) {
	fmt.Fprintf(w, "claim %s / policy %s (model %s, request %s, tokens %s in / %s out)\n",
		file.Claim.ClaimID, file.Policy.PolicyID, result.Model, orNA(result.RequestID),
		tokens(result.Usage.InputTokens), tokens(result.Usage.OutputTokens))

	fmt.Fprintf(w, "\nnoul answers, probability of yes (threshold %.2f, undecided within +/-%.2f)\n",
		yesThreshold, uncertainMargin)
	for _, name := range questionOrder {
		answer, ok := result.Nouls[name]
		if !ok {
			// A missing answer is reported, not defaulted to "no".
			fmt.Fprintf(w, "  %-16s  -  (no noul answer in response)\n", name)
			continue
		}
		// The yes/no is a display convenience; the probability, and how far it sits
		// from the threshold, is the answer worth reading.
		fmt.Fprintf(w, "  %-16s  %-3s  %.2f  %s\n", name, verdict(answer.Noul), answer.Noul, lowMargin(answer.Noul))
	}

	fmt.Fprintln(w, "\noffline cross-checks (computed from the claim file)")
	for _, c := range offlineChecks(file) {
		answer, ok := result.Nouls[c.Question]
		if !ok {
			fmt.Fprintf(w, "  %-16s  offline=%-3s  model=-\n", c.Question, yesNo(c.Answer))
			continue
		}
		status := "agree"
		switch {
		case verdict(answer.Noul) != yesNo(c.Answer):
			status = "DISAGREE"
		case lowMargin(answer.Noul) != "":
			// It matches the offline answer only by degrees.
			status = lowMargin(answer.Noul)
		}
		fmt.Fprintf(w, "  %-16s  offline=%-3s  model=%-3s  %s\n", c.Question, yesNo(c.Answer), verdict(answer.Noul), status)
		fmt.Fprintf(w, "  %-16s  %s\n", "", c.Detail)
	}

	money := calculatePayout(file)
	fmt.Fprintf(w, "\npayout arithmetic\n  claimed      %8.2f\n  ineligible   %8.2f  %s\n"+
		"  deductible   %8.2f\n  net          %8.2f\n",
		money.Claimed, money.Ineligible, joinedOrNone(money.Skipped), money.Deductible, money.Net)
	for _, note := range file.AdjusterNotes {
		fmt.Fprintf(w, "  %s said: %s\n", note.Author, note.Note)
	}
}

func main() {
	file := sampleClaim()

	// Reads TYPESAFE_API_KEY from the environment by default.
	client, err := typesafe.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := client.SystemOne(ctx, file, questions())
	if err != nil {
		log.Fatal(err)
	}
	render(os.Stdout, file, result)
}
