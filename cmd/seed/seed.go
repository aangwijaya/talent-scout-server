package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

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
	if err := domain.ValidateJob(job); err != nil {
		return err
	}
	return nil
}
