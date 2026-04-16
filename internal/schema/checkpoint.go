package schema

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// yamlCheckpointFile is the top-level structure of checkpoint.yaml.
type yamlCheckpointFile struct {
	Rules []yamlCheckpointRule `yaml:"rules"`
}

type yamlCheckpointRule struct {
	Name        string              `yaml:"name"`
	Layer       string              `yaml:"layer"`
	Severity    string              `yaml:"severity"`
	Query       string              `yaml:"query"`
	GapTemplate yamlCheckpointGap   `yaml:"gap_template"`
}

type yamlCheckpointGap struct {
	Type            string `yaml:"type"`
	Description     string `yaml:"description"`
	SuggestedAction string `yaml:"suggested_action"`
}

// LoadCheckpointRules reads an optional checkpoint.yaml from dir.
// Returns an empty slice (not an error) if the file does not exist.
func LoadCheckpointRules(dir string) ([]CheckpointRule, error) {
	path := filepath.Join(dir, "checkpoint.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading checkpoint.yaml: %w", err)
	}

	var file yamlCheckpointFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing checkpoint.yaml: %w", err)
	}

	rules := make([]CheckpointRule, 0, len(file.Rules))
	for i, yr := range file.Rules {
		if yr.Name == "" {
			return nil, fmt.Errorf("checkpoint.yaml: rule %d has empty name", i)
		}
		if yr.Layer != "universal" && yr.Layer != "particular" {
			return nil, fmt.Errorf("checkpoint.yaml: rule %q has invalid layer %q (must be 'universal' or 'particular')", yr.Name, yr.Layer)
		}
		if yr.Severity != "error" && yr.Severity != "warning" {
			return nil, fmt.Errorf("checkpoint.yaml: rule %q has invalid severity %q (must be 'error' or 'warning')", yr.Name, yr.Severity)
		}
		if yr.Query == "" {
			return nil, fmt.Errorf("checkpoint.yaml: rule %q has empty query", yr.Name)
		}
		rules = append(rules, CheckpointRule{
			Name:            yr.Name,
			Layer:           yr.Layer,
			Severity:        yr.Severity,
			Query:           yr.Query,
			GapType:         yr.GapTemplate.Type,
			GapDescription:  yr.GapTemplate.Description,
			SuggestedAction: yr.GapTemplate.SuggestedAction,
		})
	}
	return rules, nil
}
