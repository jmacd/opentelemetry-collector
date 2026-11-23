// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builders // import "go.opentelemetry.io/collector/service/internal/builders"

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/internal/componentalias"
	"go.opentelemetry.io/collector/pipeline"
)

var (
	errNilNextConsumer = errors.New("nil next Consumer")
	NopType            = component.MustNewType("nop")
)

type signalDeprecationMessageProvider interface {
	DeprecationMessage(pipeline.Signal) string
}

func logSignalStability(logger *zap.Logger, factory component.Factory, sl component.StabilityLevel, signal pipeline.Signal) {
	var note string
	if notes, ok := factory.(signalDeprecationMessageProvider); ok && sl == component.StabilityLevelDeprecated {
		note = notes.DeprecationMessage(signal)
	}
	logStabilityLevel(logger, sl, note)
}

func logStabilityLevel(logger *zap.Logger, sl component.StabilityLevel, note string) {
	message := sl.LogMessage()
	if note != "" {
		message += " " + note
	}
	if sl >= component.StabilityLevelAlpha {
		logger.Debug(message)
	} else {
		logger.Info(message)
	}
}

// logDeprecatedTypeAlias checks if the provided type is a deprecated alias and logs a warning if so.
func logDeprecatedTypeAlias(logger *zap.Logger, factory component.Factory, usedType component.Type) {
	tah, ok := factory.(componentalias.TypeAliasHolder)
	if !ok {
		return
	}
	alias := tah.DeprecatedAlias()
	if alias.String() != "" && usedType == alias {
		logger.Warn(fmt.Sprintf("%q alias is deprecated; use %q instead", alias.String(), factory.Type().String()))
	}
}
