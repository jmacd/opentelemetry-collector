// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "go.opentelemetry.io/collector/cmd/mdatagen/internal"

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/collector/component"
)

// distroURL returns the collection of distributions that can be referenced in the metadata.yaml files.
// The rules below apply to every distribution added to this list:
// - The distribution is open source and maintained by the OpenTelemetry project.
// - The link must point to a publicly accessible repository.
func distroURL(name string) string {
	switch name {
	case "core":
		return "https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol"
	case "contrib":
		return "https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-contrib"
	case "k8s":
		return "https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-k8s"
	case "otlp":
		return "https://github.com/open-telemetry/opentelemetry-collector-releases/tree/main/distributions/otelcol-otlp"
	default:
		return ""
	}
}

type Codeowners struct {
	// Active codeowners
	Active []string `mapstructure:"active"`
	// Emeritus codeowners
	Emeritus []string `mapstructure:"emeritus"`
	// Whether new codeowners are being sought
	SeekingNew bool `mapstructure:"seeking_new"`
}

type Status struct {
	Stability            StabilityMap   `mapstructure:"stability"`
	Distributions        []string       `mapstructure:"distributions"`
	Class                string         `mapstructure:"class"`
	Warnings             []string       `mapstructure:"warnings"`
	Codeowners           *Codeowners    `mapstructure:"codeowners"`
	SupportedPlatforms   []string       `mapstructure:"supported_platforms"`
	UnsupportedPlatforms []string       `mapstructure:"unsupported_platforms"`
	Deprecation          DeprecationMap `mapstructure:"deprecation"`
	CodeCovComponentID   string         `mapstructure:"codecov_component_id"`
	DisableCodeCov       bool           `mapstructure:"disable_codecov_badge"`
}

type DeprecationMap map[string]DeprecationInfo

type DeprecationInfo struct {
	Date      string `mapstructure:"date"`
	Migration string `mapstructure:"migration"`
	Level     string `mapstructure:"level"`
}

func (d DeprecationInfo) LogLevel() (string, error) {
	level := cmp.Or(d.Level, "warn")
	levels := []string{"debug", "info", "warn", "error"}
	if !slices.Contains(levels, level) {
		return "", fmt.Errorf("invalid deprecation level %q: expected one of %s", level, strings.Join(levels, ", "))
	}
	return level, nil
}

var validClasses = []string{
	"cmd",
	"docs",
	"connector",
	"converter",
	"exporter",
	"extension",
	"pkg",
	"processor",
	"provider",
	"receiver",
	"scraper",
	"internal",
}

var validStabilityKeys = []string{
	"converter",
	"extension",
	"logs",
	"logs_to_traces",
	"logs_to_metrics",
	"logs_to_logs",
	"logs_to_profiles",
	"metrics",
	"metrics_to_traces",
	"metrics_to_metrics",
	"metrics_to_logs",
	"metrics_to_profiles",
	"profiles",
	"profiles_to_profiles",
	"profiles_to_traces",
	"profiles_to_metrics",
	"profiles_to_logs",
	"provider",
	"traces_to_traces",
	"traces_to_metrics",
	"traces_to_logs",
	"traces_to_profiles",
	"traces",
}

func (s *Status) SortedDistributions() []string {
	sorted := s.Distributions
	sort.Slice(sorted, func(i, j int) bool {
		if s.Distributions[i] == "core" {
			return true
		}
		if s.Distributions[i] == "contrib" {
			return s.Distributions[j] != "core"
		}
		if s.Distributions[j] == "core" {
			return false
		}
		if s.Distributions[j] == "contrib" {
			return s.Distributions[i] == "core"
		}
		return s.Distributions[i] < s.Distributions[j]
	})
	return sorted
}

func (s *Status) Validate() error {
	var errs error
	if s == nil {
		return errors.New("missing status")
	}

	if len(s.SupportedPlatforms) > 0 && len(s.UnsupportedPlatforms) > 0 {
		errs = errors.Join(errs, errors.New(
			"supported_platforms and unsupported_platforms cannot both be specified",
		))
	}

	if err := s.validateClass(); err != nil {
		errs = errors.Join(errs, err)
	}

	if !slices.Contains(nonComponents, s.Class) {
		if err := s.Stability.Validate(); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	if err := s.validateDeprecation(); err != nil {
		errs = errors.Join(errs, err)
	}
	return errs
}

func (s *Status) validateClass() error {
	if s.Class == "" {
		return errors.New("missing class")
	}
	if !slices.Contains(validClasses, s.Class) {
		return fmt.Errorf("invalid class: %v", s.Class)
	}
	return nil
}

type StabilityMap map[component.StabilityLevel][]string

func (ms StabilityMap) Validate() error {
	var errs error
	if len(ms) == 0 {
		return errors.New("missing stability")
	}
	for stability, cmps := range ms {
		if len(cmps) == 0 {
			errs = errors.Join(errs, fmt.Errorf("missing component for stability: %v", stability))
		}
		for _, c := range cmps {
			if !slices.Contains(validStabilityKeys, c) {
				errs = errors.Join(errs, fmt.Errorf("invalid component: %v", c))
			}
		}
	}
	return errs
}

func (s *Status) validateDeprecation() error {
	var errs error
	deprecated := s.Stability[component.StabilityLevelDeprecated]
	for _, c := range deprecated {
		if _, ok := s.Deprecation[c]; !ok {
			errs = errors.Join(errs, fmt.Errorf("deprecated component missing deprecation date and migration guide for %v", c))
		}
	}
	for c, info := range s.Deprecation {
		if !slices.Contains(deprecated, c) {
			errs = errors.Join(errs, fmt.Errorf("deprecation of %q: not listed in stability.deprecated", c))
			continue
		}
		if !s.validDeprecationTarget(c) {
			errs = errors.Join(errs, fmt.Errorf("invalid deprecated component: %q", c))
			continue
		}
		if _, err := info.LogLevel(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("deprecation of %s: %w", c, err))
		}
		if strings.TrimSpace(info.Migration) == "" {
			errs = errors.Join(errs, fmt.Errorf("deprecated component missing migration guide: %v", c))
		}
		if info.Date == "" {
			errs = errors.Join(errs, fmt.Errorf("deprecated component missing date in YYYY-MM-DD format: %v", c))
		} else if _, err := time.Parse(time.DateOnly, info.Date); err != nil {
			errs = errors.Join(errs, fmt.Errorf("deprecated component missing valid date in YYYY-MM-DD format: %v", c))
		}
	}
	return errs
}

func (s *Status) validDeprecationTarget(target string) bool {
	switch s.Class {
	case "converter", "extension", "provider":
		return target == s.Class
	case "connector":
		return slices.Contains(validStabilityKeys, target) && strings.Contains(target, "_to_")
	case "scraper":
		return target == "logs" || target == "metrics" || target == "profiles"
	case "exporter", "processor", "receiver":
		return target == "traces" || target == "metrics" || target == "logs" || target == "profiles"
	default:
		return target == s.Class || slices.Contains(validStabilityKeys, target)
	}
}
