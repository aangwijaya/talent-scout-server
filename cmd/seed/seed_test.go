package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

func validJob() domain.Job {
	return domain.Job{
		ID:          "7c9e6679-7425-40de-944b-e07fc1f90ae7",
		Title:       "Senior Frontend Engineer",
		Description: strings.Repeat("a", 60),
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Description: "d", Weight: 2},
			{Key: "system_design", Label: "System design", Description: "d", Weight: 2},
			{Key: "communication", Label: "Communication", Description: "d", Weight: 1},
		},
		Status: domain.JobStatusOpen,
	}
}

func TestValidateJob(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*domain.Job)
		wantErr string
	}{
		{name: "valid"},
		{
			name:    "missing id",
			mutate:  func(j *domain.Job) { j.ID = "" },
			wantErr: "id is required",
		},
		{
			name:    "invalid id",
			mutate:  func(j *domain.Job) { j.ID = "not-a-uuid" },
			wantErr: "not a valid UUID",
		},
		{
			name:    "title too short",
			mutate:  func(j *domain.Job) { j.Title = "ab" },
			wantErr: "title must be 3-120",
		},
		{
			name:    "title too long",
			mutate:  func(j *domain.Job) { j.Title = strings.Repeat("a", 121) },
			wantErr: "title must be 3-120",
		},
		{
			name:    "description too short",
			mutate:  func(j *domain.Job) { j.Description = "short" },
			wantErr: "description must be 50-8000",
		},
		{
			name:    "description too long",
			mutate:  func(j *domain.Job) { j.Description = strings.Repeat("a", 8001) },
			wantErr: "description must be 50-8000",
		},
		{
			name: "too few criteria",
			mutate: func(j *domain.Job) {
				j.Rubric = j.Rubric[:2]
			},
			wantErr: "rubric must have 3-6",
		},
		{
			name: "too many criteria",
			mutate: func(j *domain.Job) {
				for i := 0; i < 4; i++ {
					j.Rubric = append(j.Rubric, domain.RubricCriterion{Key: "extra_" + string(rune('a'+i)), Label: "x", Description: "d", Weight: 1})
				}
			},
			wantErr: "rubric must have 3-6",
		},
		{
			name: "duplicate key",
			mutate: func(j *domain.Job) {
				j.Rubric[1].Key = j.Rubric[0].Key
			},
			wantErr: "duplicated",
		},
		{
			name: "invalid key",
			mutate: func(j *domain.Job) {
				j.Rubric[0].Key = "Frontend"
			},
			wantErr: "must match",
		},
		{
			name: "key too long",
			mutate: func(j *domain.Job) {
				j.Rubric[0].Key = "a" + strings.Repeat("b", 40)
			},
			wantErr: "must match",
		},
		{
			name: "weight too low",
			mutate: func(j *domain.Job) {
				j.Rubric[0].Weight = 0
			},
			wantErr: "weight must be 1-3",
		},
		{
			name: "weight too high",
			mutate: func(j *domain.Job) {
				j.Rubric[0].Weight = 4
			},
			wantErr: "weight must be 1-3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := validJob()
			if tc.mutate != nil {
				tc.mutate(&job)
			}
			err := validateJob(job)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateJob() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateJob() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestSeedFileIsValid(t *testing.T) {
	job, err := loadJob(filepath.Join("..", "..", "config", "seed-job.yaml"))
	if err != nil {
		t.Fatalf("loadJob: %v", err)
	}
	if err := validateJob(job); err != nil {
		t.Fatalf("seed job is invalid: %v", err)
	}
}
