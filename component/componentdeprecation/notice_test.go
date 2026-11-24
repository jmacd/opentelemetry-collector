// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package componentdeprecation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestConflictingDeclarations(t *testing.T) {
	MustRegister("receiver", "conflict", "logs", zap.WarnLevel, "Use first.")
	MustRegister("receiver", "conflict", "logs", zap.WarnLevel, "Use second.")
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	assert.False(t, Log(logger, "receiver", "conflict", "metrics"))
	assert.True(t, Log(logger, "receiver", "conflict", "logs"))
	entries := logs.All()
	require.Len(t, entries, 1)
	assert.Equal(t, zap.ErrorLevel, entries[0].Level)
	assert.Contains(t, entries[0].Message, "Conflicting deprecation notices for receiver/conflict: logs")
}
