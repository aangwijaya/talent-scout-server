package domain

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// RubricKeyPattern is the allowed shape of a rubric criterion key. It is shared
// by the seed, job create/edit, and rubric suggestions so the rules cannot drift.
var RubricKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

// ValidationError is one rejected input field. Field names the request field
// (for example "rubric[2].weight") so the API can return it in details.field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func validationError(field, message string) *ValidationError {
	return &ValidationError{Field: field, Message: message}
}

// ValidateJobTitle checks the title rule shared by create, edit, and the seed.
func ValidateJobTitle(title string) *ValidationError {
	if n := utf8.RuneCountInString(title); n < 3 || n > 120 {
		return validationError("title", fmt.Sprintf("title must be 3-120 characters, got %d", n))
	}
	return nil
}

// ValidateJobDescription checks the description rule shared by create, edit,
// and the seed.
func ValidateJobDescription(description string) *ValidationError {
	if n := utf8.RuneCountInString(description); n < 50 || n > 12000 {
		return validationError("description", fmt.Sprintf("description must be 50-12000 characters, got %d", n))
	}
	return nil
}

// ValidateJobStatus checks that status is open or closed.
func ValidateJobStatus(status JobStatus) *ValidationError {
	if status != JobStatusOpen && status != JobStatusClosed {
		return validationError("status", "status must be open or closed")
	}
	return nil
}

// ValidateRubric checks the rubric shape: 3-6 criteria with unique keys, a
// bounded label and description, and an integer weight from 1 to 3.
func ValidateRubric(rubric []RubricCriterion) *ValidationError {
	if n := len(rubric); n < 3 || n > 6 {
		return validationError("rubric", fmt.Sprintf("rubric must have 3-6 criteria, got %d", n))
	}
	seen := make(map[string]struct{}, len(rubric))
	for i, criterion := range rubric {
		keyField := fmt.Sprintf("rubric[%d].key", i)
		if !RubricKeyPattern.MatchString(criterion.Key) {
			return validationError(keyField, fmt.Sprintf("rubric[%d].key %q must match %s", i, criterion.Key, RubricKeyPattern))
		}
		if _, ok := seen[criterion.Key]; ok {
			return validationError(keyField, fmt.Sprintf("rubric[%d].key %q is duplicated", i, criterion.Key))
		}
		seen[criterion.Key] = struct{}{}
		if n := utf8.RuneCountInString(criterion.Label); n < 2 || n > 60 {
			return validationError(fmt.Sprintf("rubric[%d].label", i), fmt.Sprintf("rubric[%d].label must be 2-60 characters, got %d", i, n))
		}
		if n := utf8.RuneCountInString(criterion.Description); n < 10 || n > 300 {
			return validationError(fmt.Sprintf("rubric[%d].description", i), fmt.Sprintf("rubric[%d].description must be 10-300 characters, got %d", i, n))
		}
		if criterion.Weight < 1 || criterion.Weight > 3 {
			return validationError(fmt.Sprintf("rubric[%d].weight", i), fmt.Sprintf("rubric[%d].weight must be 1-3, got %d", i, criterion.Weight))
		}
	}
	return nil
}

// ValidateJob checks every job field with the shared rules.
func ValidateJob(job Job) *ValidationError {
	if err := ValidateJobTitle(job.Title); err != nil {
		return err
	}
	if err := ValidateJobDescription(job.Description); err != nil {
		return err
	}
	if err := ValidateRubric(job.Rubric); err != nil {
		return err
	}
	if err := ValidateJobStatus(job.Status); err != nil {
		return err
	}
	return nil
}
