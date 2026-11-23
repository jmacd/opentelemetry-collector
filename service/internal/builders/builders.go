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

// deprecationMessageProvider supplies migration guidance for a signal, an ordered
// input/output signal pair (connectors), or no signals (extensions).
type deprecationMessageProvider interface {
	DeprecationMessage(signals ...pipeline.Signal) string
}

// logStabilityLevel logs the stability level of a component. The log level is set to info for
// undefined, unmaintained, deprecated and development. The log level is set to debug
// for alpha, beta and stable.
func logStabilityLevel(logger *zap.Logger, factory component.Factory, sl component.StabilityLevel, signals ...pipeline.Signal) {
	message := sl.LogMessage()
	if notes, ok := factory.(deprecationMessageProvider); ok && sl == component.StabilityLevelDeprecated {
		if note := notes.DeprecationMessage(signals...); note != "" {
			message += " " + note
		}
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
