// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builders // import "go.opentelemetry.io/collector/service/internal/builders"

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentdeprecation"
	"go.opentelemetry.io/collector/internal/componentalias"
)

var (
	errNilNextConsumer = errors.New("nil next Consumer")
	NopType            = component.MustNewType("nop")
)

// A registered migration notice replaces the generic deprecated status message.
// Other components retain their usual stability logging.
func logStabilityLevel(logger *zap.Logger, sl component.StabilityLevel, class string, factory component.Factory, target string) {
	if componentdeprecation.Log(logger, class, factory.Type().String(), target) {
		return
	}
	if sl >= component.StabilityLevelAlpha {
		logger.Debug(sl.LogMessage())
	} else {
		logger.Info(sl.LogMessage())
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
