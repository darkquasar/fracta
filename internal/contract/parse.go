package contract

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ParseContract parses a contract.yaml from a YAML byte slice.
func ParseContract(data []byte) (*ContractSpec, error) {
	var cs ContractSpec
	if err := yaml.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("parsing contract YAML: %w", err)
	}
	if err := validateContract(&cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// ParseContractFile reads and parses a contract.yaml from disk.
func ParseContractFile(path string) (*ContractSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading contract file: %w", err)
	}
	return ParseContract(data)
}

// validateContract checks required fields in a parsed contract.
func validateContract(cs *ContractSpec) error {
	if cs.Name == "" {
		return fmt.Errorf("contract must have a 'name' field")
	}
	if cs.Description == "" {
		return fmt.Errorf("contract must have a 'description' field")
	}
	if len(cs.Tags) == 0 {
		return fmt.Errorf("contract must have at least one tag")
	}
	return nil
}
