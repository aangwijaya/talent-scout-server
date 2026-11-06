package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

var (
	rubricKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
	uuidPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type seedJob struct {
	ID          string                   `yaml:"id"`
	Title       string                   `yaml:"title"`
	Description string                   `yaml:"description"`
	Rubric      []domain.RubricCriterion `yaml:"rubric"`
}

func loadJob(path string) (domain.Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Job{}, fmt.Errorf("read seed job: %w", err)
	}
	var raw seedJob
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return domain.Job{}, fmt.Errorf("parse seed job: %w", err)
	}
	return domain.Job{
		ID:          raw.ID,
		Title:       raw.Title,
		Description: raw.Description,
		Rubric:      raw.Rubric,
		Status:      domain.JobStatusOpen,
	}, nil
}

func validateJob(job domain.Job) error {
	if job.ID == "" {
		return errors.New("id is required")
	}
	if !uuidPattern.MatchString(job.ID) {
		return fmt.Errorf("id %q is not a valid UUID", job.ID)
	}

	if n := utf8.RuneCountInString(job.Title); n < 3 || n > 120 {
		return fmt.Errorf("title must be 3-120 characters, got %d", n)
	}
	if n := utf8.RuneCountInString(job.Description); n < 50 || n > 8000 {
		return fmt.Errorf("description must be 50-8000 characters, got %d", n)
	}
	if n := len(job.Rubric); n < 3 || n > 6 {
		return fmt.Errorf("rubric must have 3-6 criteria, got %d", n)
	}

	seen := make(map[string]struct{}, len(job.Rubric))
	for i, criterion := range job.Rubric {
		if !rubricKeyPattern.MatchString(criterion.Key) {
			return fmt.Errorf("rubric[%d].key %q must match %s", i, criterion.Key, rubricKeyPattern)
		}
		if _, ok := seen[criterion.Key]; ok {
			return fmt.Errorf("rubric key %q is duplicated", criterion.Key)
		}
		seen[criterion.Key] = struct{}{}
		if criterion.Weight < 1 || criterion.Weight > 3 {
			return fmt.Errorf("rubric[%d].weight must be 1-3, got %d", i, criterion.Weight)
		}
	}
	return nil
}
