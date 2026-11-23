// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builders

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/internal/componentalias"
	"go.opentelemetry.io/collector/pipeline"
	"go.opentelemetry.io/collector/pipeline/xpipeline"
)

// mockFactory is a test factory that implements component.Factory
type mockFactory struct {
	factoryType component.Type
}

func (m *mockFactory) Type() component.Type {
	return m.factoryType
}

func (m *mockFactory) CreateDefaultConfig() component.Config {
	return nil
}

type factoryWithNotes struct{ mockFactory }

func (*factoryWithNotes) DeprecationMessage(signals ...pipeline.Signal) string {
	switch {
	case len(signals) == 0:
		return "Migrate the extension."
	case slices.Equal(signals, []pipeline.Signal{pipeline.SignalLogs}):
		return "See https://example.org/migration"
	case slices.Equal(signals, []pipeline.Signal{pipeline.SignalLogs, xpipeline.SignalProfiles}):
		return "Migrate the logs-to-profiles connector."
	}
	return ""
}

func TestLogStabilityLevelWithMigration(t *testing.T) {
	deprecated := component.StabilityLevelDeprecated
	f := &factoryWithNotes{}
	for _, tt := range []struct {
		name    string
		factory component.Factory
		signals []pipeline.Signal
		sl      component.StabilityLevel
		level   zapcore.Level
		want    string
	}{
		{"signal", f, []pipeline.Signal{pipeline.SignalLogs}, deprecated, zap.InfoLevel, deprecated.LogMessage() + " See https://example.org/migration"},
		{"other signal", f, []pipeline.Signal{pipeline.SignalTraces}, deprecated, zap.InfoLevel, deprecated.LogMessage()},
		{"connector", f, []pipeline.Signal{pipeline.SignalLogs, xpipeline.SignalProfiles}, deprecated, zap.InfoLevel, deprecated.LogMessage() + " Migrate the logs-to-profiles connector."},
		{"reverse connector", f, []pipeline.Signal{xpipeline.SignalProfiles, pipeline.SignalLogs}, deprecated, zap.InfoLevel, deprecated.LogMessage()},
		{"extension", f, nil, deprecated, zap.InfoLevel, deprecated.LogMessage() + " Migrate the extension."},
		{"no provider", &mockFactory{}, nil, deprecated, zap.InfoLevel, deprecated.LogMessage()},
		{"not deprecated", f, []pipeline.Signal{pipeline.SignalLogs}, component.StabilityLevelBeta, zap.DebugLevel, component.StabilityLevelBeta.LogMessage()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zap.DebugLevel)
			logStabilityLevel(zap.New(core), tt.factory, tt.sl, tt.signals...)
			entries := logs.All()
			require.Len(t, entries, 1)
			require.Equal(t, tt.level, entries[0].Level)
			require.Equal(t, tt.want, entries[0].Message)
		})
	}
}

// mockFactoryWithAlias is a test factory that implements both component.Factory and componentalias.TypeAliasHolder
type mockFactoryWithAlias struct {
	factoryType component.Type
	aliasHolder componentalias.TypeAliasHolder
}

func (m *mockFactoryWithAlias) Type() component.Type {
	return m.factoryType
}

func (m *mockFactoryWithAlias) CreateDefaultConfig() component.Config {
	return nil
}

func (m *mockFactoryWithAlias) DeprecatedAlias() component.Type {
	return m.aliasHolder.DeprecatedAlias()
}

func (m *mockFactoryWithAlias) SetDeprecatedAlias(alias component.Type) {
	m.aliasHolder.SetDeprecatedAlias(alias)
}

func TestLogDeprecatedTypeAlias(t *testing.T) {
	tests := []struct {
		name          string
		factory       component.Factory
		usedType      component.Type
		expectWarning bool
	}{
		{
			name:          "no_alias_holder",
			factory:       &mockFactory{factoryType: component.MustNewType("test")},
			usedType:      component.MustNewType("test"),
			expectWarning: false,
		},
		{
			name: "no_alias_set",
			factory: &mockFactoryWithAlias{
				factoryType: component.MustNewType("test"),
				aliasHolder: componentalias.NewTypeAliasHolder(),
			},
			usedType:      component.MustNewType("test"),
			expectWarning: false,
		},
		{
			name: "using_current_type",
			factory: func() component.Factory {
				f := &mockFactoryWithAlias{
					factoryType: component.MustNewType("new"),
					aliasHolder: componentalias.NewTypeAliasHolder(),
				}
				f.aliasHolder.SetDeprecatedAlias(component.MustNewType("old"))
				return f
			}(),
			usedType:      component.MustNewType("new"),
			expectWarning: false,
		},
		{
			name: "using_deprecated_alias",
			factory: func() component.Factory {
				f := &mockFactoryWithAlias{
					factoryType: component.MustNewType("new"),
					aliasHolder: componentalias.NewTypeAliasHolder(),
				}
				f.aliasHolder.SetDeprecatedAlias(component.MustNewType("old"))
				return f
			}(),
			usedType:      component.MustNewType("old"),
			expectWarning: true,
		},
		{
			name: "using_unrelated_type",
			factory: func() component.Factory {
				f := &mockFactoryWithAlias{
					factoryType: component.MustNewType("new"),
					aliasHolder: componentalias.NewTypeAliasHolder(),
				}
				f.aliasHolder.SetDeprecatedAlias(component.MustNewType("old"))
				return f
			}(),
			usedType:      component.MustNewType("other"),
			expectWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			logger := zap.New(core)

			logDeprecatedTypeAlias(logger, tt.factory, tt.usedType)

			if tt.expectWarning && logs.Len() != 1 {
				t.Errorf("expected 1 warning log but got %d", logs.Len())
			} else if !tt.expectWarning && logs.Len() > 0 {
				t.Errorf("expected no warning log but got %d", logs.Len())
			}
		})
	}
}
