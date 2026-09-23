// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import "go.opentelemetry.io/collector/featuregate"

// FeatureGate enables the integrated experimental logs payload path.
var FeatureGate = featuregate.GlobalRegistry().MustRegister(
	"service.PluggableLogs", featuregate.StageAlpha,
	featuregate.WithRegisterDescription("Use native logs payloads and views through compatible pipeline components."),
)
