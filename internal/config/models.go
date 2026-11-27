package config

import (
	"fmt"
	"math"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ModelsConfig mirrors config/models.yaml: the model assigned to each graph node,
// shared defaults, per-model prices used to record cost, and the speech-to-text
// model with its hourly price.
type ModelsConfig struct {
	Default     Defaults          `yaml:"default"`
	Nodes       map[string]Node   `yaml:"nodes"`
	Prices      map[string]Price  `yaml:"prices"`
	Transcriber TranscriberConfig `yaml:"transcriber"`
}

// TranscriberConfig is the speech-to-text model and its billing rule.
type TranscriberConfig struct {
	Model            string  `yaml:"model"`
	USDPerHour       float64 `yaml:"usd_per_hour"`
	MinBilledSeconds int     `yaml:"min_billed_seconds"`
}

// Defaults are shared across nodes unless a node overrides them.
type Defaults struct {
	Temperature           float64 `yaml:"temperature"`
	TimeoutSeconds        int     `yaml:"timeout_seconds"`
	MaxRetriesOnHTTPError int     `yaml:"max_retries_on_http_error"`
}

// Node is the model config for a single graph node (planner, interviewer, …).
type Node struct {
	Model       string           `yaml:"model"`
	Temperature *float64         `yaml:"temperature"`
	MaxTokens   int              `yaml:"max_tokens"`
	Stream      bool             `yaml:"stream"`
	Reasoning   *ReasoningConfig `yaml:"reasoning"`
}

// ReasoningConfig is passed through to the model as the `reasoning` field.
type ReasoningConfig struct {
	Effort string `yaml:"effort"` // low | medium | high
}

// Price is the OpenRouter price per 1M tokens (input/output) in USD.
type Price struct {
	In  float64 `yaml:"in"`
	Out float64 `yaml:"out"`
}

// LoadModels reads and validates the models config at path.
func LoadModels(path string) (ModelsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ModelsConfig{}, fmt.Errorf("read models config: %w", err)
	}
	var mc ModelsConfig
	if err := yaml.Unmarshal(data, &mc); err != nil {
		return ModelsConfig{}, fmt.Errorf("parse models config: %w", err)
	}
	if err := mc.validate(); err != nil {
		return ModelsConfig{}, err
	}
	return mc, nil
}

func (c ModelsConfig) validate() error {
	if c.Default.MaxRetriesOnHTTPError < 0 {
		return fmt.Errorf("default.max_retries_on_http_error must be >= 0, got %d", c.Default.MaxRetriesOnHTTPError)
	}
	if len(c.Nodes) == 0 {
		return fmt.Errorf("models config must define at least one node")
	}
	for id, node := range c.Nodes {
		if node.Model == "" {
			return fmt.Errorf("nodes.%s.model must not be empty", id)
		}
		if _, ok := c.Prices[node.Model]; !ok {
			return fmt.Errorf("nodes.%s.model %q has no price in prices", id, node.Model)
		}
	}
	for model, price := range c.Prices {
		if price.In <= 0 || price.Out <= 0 {
			return fmt.Errorf("prices.%s must be positive, got %+v", model, price)
		}
	}
	if c.Transcriber.Model != "" {
		if c.Transcriber.USDPerHour <= 0 {
			return fmt.Errorf("transcriber.usd_per_hour must be positive, got %v", c.Transcriber.USDPerHour)
		}
		if c.Transcriber.MinBilledSeconds < 0 {
			return fmt.Errorf("transcriber.min_billed_seconds must be >= 0, got %d", c.Transcriber.MinBilledSeconds)
		}
	}
	return nil
}

// Node returns the model config for nodeID.
func (c ModelsConfig) Node(nodeID string) (Node, error) {
	node, ok := c.Nodes[nodeID]
	if !ok {
		return Node{}, fmt.Errorf("no model config for node %q", nodeID)
	}
	return node, nil
}

// Temperature returns the node override, or the shared default.
func (c ModelsConfig) Temperature(nodeID string) float64 {
	if node, ok := c.Nodes[nodeID]; ok && node.Temperature != nil {
		return *node.Temperature
	}
	return c.Default.Temperature
}

// Timeout returns the shared request timeout.
func (c ModelsConfig) Timeout() time.Duration {
	if c.Default.TimeoutSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.Default.TimeoutSeconds) * time.Second
}

// MaxRetries returns the shared HTTP retry count (retries after the first
// attempt, so a value of 1 means 2 attempts total).
func (c ModelsConfig) MaxRetries() int {
	return c.Default.MaxRetriesOnHTTPError
}

// PriceFor returns the price for a model id.
func (c ModelsConfig) PriceFor(model string) (Price, bool) {
	p, ok := c.Prices[model]
	return p, ok
}

// CostUSD computes the cost of a model call in USD given token usage. A missing
// price yields 0 so recording never fails.
func (c ModelsConfig) CostUSD(model string, tokensIn, tokensOut int) float64 {
	price, ok := c.Prices[model]
	if !ok {
		return 0
	}
	return (float64(tokensIn)*price.In + float64(tokensOut)*price.Out) / 1_000_000
}

// TranscriptionCostUSD bills a recording by whole seconds at the configured
// hourly price, never below the provider's minimum billed seconds.
func (c ModelsConfig) TranscriptionCostUSD(durationMS int) float64 {
	if c.Transcriber.Model == "" || c.Transcriber.USDPerHour <= 0 {
		return 0
	}
	billedSeconds := math.Ceil(float64(durationMS) / 1000)
	if minimum := float64(c.Transcriber.MinBilledSeconds); billedSeconds < minimum {
		billedSeconds = minimum
	}
	return billedSeconds / 3600 * c.Transcriber.USDPerHour
}
