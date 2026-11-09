package graph

import (
	"fmt"
	"strings"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// evidenceCheckName is the check name used by check.passed/check.failed for the
// scorer's evidence validation (docs/EVENTS.md §4).
const evidenceCheckName = "evidence_quotes"

// evidenceCheckReason is the only reason docs/EVENTS.md documents for a failed
// evidence check. Domain-specific reasons (quote_too_short, quote_too_long) are
// deliberately mapped onto it because the client renders this value.
const evidenceCheckReason = "quote_not_found"

// ScorerEvidence is one evidence entry in the scorer's JSON reply. MessageID is
// a short alias such as "m1" so the model never has to copy a UUID.
type ScorerEvidence struct {
	MessageID string `json:"message_id"`
	Quote     string `json:"quote"`
}

// ScorerScore is one criterion score in the scorer's JSON reply.
type ScorerScore struct {
	RubricKey string           `json:"rubric_key"`
	Score     *int             `json:"score"`
	Rationale string           `json:"rationale"`
	Evidence  []ScorerEvidence `json:"evidence"`
}

// ScorerOutput is the scorer's JSON reply.
type ScorerOutput struct {
	Scores []ScorerScore `json:"scores"`
}

type evidenceIssue struct {
	RubricKey string
	Reason    string
	Quote     string
	Detail    string
}

type evidenceResult struct {
	Valid  map[string][]domain.ScoreEvidence
	Errors []evidenceIssue
}

// aliasTable maps the short message aliases given to the model back to the
// stored applicant messages. Aliases are assigned only to applicant messages,
// because only those can provide evidence.
type aliasTable struct {
	byAlias map[string]domain.Message
	byID    map[string]domain.Message
	aliasOf map[string]string
}

func newAliasTable(messages []domain.Message) aliasTable {
	table := aliasTable{
		byAlias: make(map[string]domain.Message),
		byID:    make(map[string]domain.Message),
		aliasOf: make(map[string]string),
	}
	next := 0
	for _, message := range messages {
		table.byID[message.ID] = message
		if message.Role != domain.MessageRoleApplicant {
			continue
		}
		next++
		alias := fmt.Sprintf("m%d", next)
		table.byAlias[alias] = message
		table.aliasOf[message.ID] = alias
	}
	return table
}

func (t aliasTable) alias(messageID string) string {
	return t.aliasOf[messageID]
}

func (t aliasTable) resolve(messageID string) (domain.Message, bool) {
	if message, ok := t.byAlias[messageID]; ok {
		return message, true
	}
	message, ok := t.byID[messageID]
	return message, ok
}

// checkEvidence validates the scorer output for keys against ARCHITECTURE §2:
// one score per criterion, an integer 1-5, at least one evidence entry, and
// every quote (whitespace-normalized and lowercased) a substring of the
// referenced role=applicant message. Quotes must also pass
// domain.EvidenceQuoteRejectReason (3-300 characters, at least 3 words).
func checkEvidence(keys []string, scores map[string]ScorerScore, duplicates map[string]struct{}, aliases aliasTable) evidenceResult {
	result := evidenceResult{Valid: make(map[string][]domain.ScoreEvidence)}
	for _, key := range keys {
		if _, duplicate := duplicates[key]; duplicate {
			result.Errors = append(result.Errors, evidenceIssue{RubricKey: key, Reason: evidenceCheckReason, Detail: "more than one score was returned for this criterion"})
			continue
		}
		score, ok := scores[key]
		if !ok {
			result.Errors = append(result.Errors, evidenceIssue{RubricKey: key, Reason: evidenceCheckReason, Detail: "no score was returned for this criterion"})
			continue
		}
		if score.Score == nil || *score.Score < 1 || *score.Score > 5 {
			result.Errors = append(result.Errors, evidenceIssue{RubricKey: key, Reason: evidenceCheckReason, Detail: "score must be an integer from 1 to 5"})
			continue
		}
		if len(score.Evidence) == 0 {
			result.Errors = append(result.Errors, evidenceIssue{RubricKey: key, Reason: evidenceCheckReason, Detail: "at least one evidence quote is required"})
			continue
		}

		resolved := make([]domain.ScoreEvidence, 0, len(score.Evidence))
		valid := true
		for _, evidence := range score.Evidence {
			message, found := aliases.resolve(evidence.MessageID)
			if !found || message.Role != domain.MessageRoleApplicant {
				result.Errors = append(result.Errors, evidenceIssue{RubricKey: key, Reason: evidenceCheckReason, Quote: evidence.Quote, Detail: "message_id does not reference an applicant message"})
				valid = false
				continue
			}
			if domain.EvidenceQuoteRejectReason(evidence.Quote) != "" {
				result.Errors = append(result.Errors, evidenceIssue{RubricKey: key, Reason: evidenceCheckReason, Quote: evidence.Quote, Detail: "quote must be 3-300 characters and at least 3 words"})
				valid = false
				continue
			}
			if !strings.Contains(domain.NormalizeEvidence(message.Content), domain.NormalizeEvidence(evidence.Quote)) {
				result.Errors = append(result.Errors, evidenceIssue{RubricKey: key, Reason: evidenceCheckReason, Quote: evidence.Quote, Detail: "quote is not a substring of the referenced applicant message"})
				valid = false
				continue
			}
			resolved = append(resolved, domain.ScoreEvidence{MessageID: message.ID, Quote: evidence.Quote})
		}
		if valid {
			result.Valid[key] = resolved
		}
	}
	return result
}
