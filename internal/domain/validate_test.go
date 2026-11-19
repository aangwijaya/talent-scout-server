package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

func validJob() domain.Job {
	return domain.Job{
		Title:       "Senior Frontend Engineer",
		Description: strings.Repeat("a", 60),
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte and React depth.", Weight: 3},
			{Key: "system_design", Label: "System design", Description: "Component boundaries.", Weight: 2},
			{Key: "communication", Label: "Communication", Description: "Clarity and collaboration.", Weight: 1},
		},
		Status: domain.JobStatusOpen,
	}
}

func TestValidateJobFields(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*domain.Job)
		wantField string
	}{
		{name: "valid"},
		{name: "title too short", mutate: func(j *domain.Job) { j.Title = "ab" }, wantField: "title"},
		{name: "title too long", mutate: func(j *domain.Job) { j.Title = strings.Repeat("a", 121) }, wantField: "title"},
		{name: "description too short", mutate: func(j *domain.Job) { j.Description = strings.Repeat("a", 49) }, wantField: "description"},
		{name: "description too long", mutate: func(j *domain.Job) { j.Description = strings.Repeat("a", 12001) }, wantField: "description"},
		{name: "description at new limit", mutate: func(j *domain.Job) { j.Description = strings.Repeat("a", 12000) }},
		{name: "too few criteria", mutate: func(j *domain.Job) { j.Rubric = j.Rubric[:2] }, wantField: "rubric"},
		{name: "too many criteria", mutate: func(j *domain.Job) {
			j.Rubric = append(j.Rubric,
				domain.RubricCriterion{Key: "extra_a", Label: "Extra A", Description: "An extra criterion.", Weight: 1},
				domain.RubricCriterion{Key: "extra_b", Label: "Extra B", Description: "Another criterion.", Weight: 1},
				domain.RubricCriterion{Key: "extra_c", Label: "Extra C", Description: "A third criterion.", Weight: 1},
				domain.RubricCriterion{Key: "extra_d", Label: "Extra D", Description: "A fourth criterion.", Weight: 1},
			)
		}, wantField: "rubric"},
		{name: "invalid key", mutate: func(j *domain.Job) { j.Rubric[0].Key = "Frontend" }, wantField: "rubric[0].key"},
		{name: "duplicate key", mutate: func(j *domain.Job) { j.Rubric[1].Key = j.Rubric[0].Key }, wantField: "rubric[1].key"},
		{name: "label too short", mutate: func(j *domain.Job) { j.Rubric[0].Label = "x" }, wantField: "rubric[0].label"},
		{name: "label too long", mutate: func(j *domain.Job) { j.Rubric[0].Label = strings.Repeat("a", 61) }, wantField: "rubric[0].label"},
		{name: "criterion description too short", mutate: func(j *domain.Job) { j.Rubric[0].Description = "short" }, wantField: "rubric[0].description"},
		{name: "criterion description too long", mutate: func(j *domain.Job) { j.Rubric[0].Description = strings.Repeat("a", 301) }, wantField: "rubric[0].description"},
		{name: "weight too low", mutate: func(j *domain.Job) { j.Rubric[0].Weight = 0 }, wantField: "rubric[0].weight"},
		{name: "weight too high", mutate: func(j *domain.Job) { j.Rubric[0].Weight = 4 }, wantField: "rubric[0].weight"},
		{name: "invalid status", mutate: func(j *domain.Job) { j.Status = "paused" }, wantField: "status"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := validJob()
			if tc.mutate != nil {
				tc.mutate(&job)
			}
			err := domain.ValidateJob(job)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("ValidateJob() error = %v, want nil", err)
				}
				return
			}
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("ValidateJob() error = %v, want *ValidationError", err)
			}
			if verr.Field != tc.wantField {
				t.Fatalf("field = %q, want %q", verr.Field, tc.wantField)
			}
		})
	}
}
