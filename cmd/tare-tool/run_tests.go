package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/DataDog/tare/internal/oci"
	"github.com/DataDog/tare/internal/testexec"
	"github.com/DataDog/tare/internal/testplan"
)

func runTests(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: tare-tool run-tests <plan-file>")
		return 1
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading plan: %v\n", err)
		return 1
	}

	var plan testplan.Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		fmt.Fprintf(os.Stderr, "error: parsing plan: %v\n", err)
		return 1
	}

	if len(plan.Tests) == 0 {
		fmt.Fprintln(os.Stderr, "error: no tests in plan")
		return 1
	}

	meta, err := loadMetadata(plan.MetadataFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: loading metadata: %v\n", err)
		return 1
	}

	failures := testexec.Run(os.Stdout, &plan, meta, testexec.Options{})

	if failures > 0 {
		return 1
	}
	return 0
}

// loadMetadata reads image metadata from the docker-inspect-format JSON file
// injected into the container by the harness.
func loadMetadata(path string) (*oci.ImageConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var inspect []struct {
		Config       *oci.ImageConfig `json:"Config"`
		Architecture string           `json:"Architecture"`
	}
	if err := json.Unmarshal(data, &inspect); err != nil {
		return nil, err
	}
	if len(inspect) == 0 || inspect[0].Config == nil {
		return &oci.ImageConfig{}, nil
	}
	cfg := inspect[0].Config
	cfg.Architecture = inspect[0].Architecture
	return cfg, nil
}
