// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builders

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/internal/componentalias"
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

func (*factoryWithNotes) DeprecationMessage(key string) string {
	if key == "logs" {
		return "See https://example.org/migration"
	}
	return ""
}

func TestLogStabilityLevelWithMigration(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	deprecated := component.StabilityLevelDeprecated
	f := &factoryWithNotes{}

	logStabilityLevel(logger, f, "logs", deprecated)
	logStabilityLevel(logger, f, "traces", deprecated)
	logStabilityLevel(logger, &mockFactory{}, "logs", deprecated)
	logStabilityLevel(logger, f, "logs", component.StabilityLevelBeta)

	entries := logs.TakeAll()
	require.Len(t, entries, 4)
	require.Equal(t, zap.InfoLevel, entries[0].Level)
	require.Equal(t, deprecated.LogMessage()+" See https://example.org/migration", entries[0].Message)
	require.Equal(t, deprecated.LogMessage(), entries[1].Message)
	require.Equal(t, deprecated.LogMessage(), entries[2].Message)
	require.Equal(t, zap.DebugLevel, entries[3].Level)
	require.Equal(t, component.StabilityLevelBeta.LogMessage(), entries[3].Message)
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
