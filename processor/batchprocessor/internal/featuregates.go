// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "go.opentelemetry.io/collector/processor/batchprocessor/internal"

import (
	"go.opentelemetry.io/collector/featuregate"
)

const (
	// UseExporterHelperGate controls whether to use exporterhelper components
	// for batching instead of the legacy implementation.
	UseExporterHelperGate = "processor.batch.useexporterhelper"

	// PropagateErrorsGate controls whether to propagate errors from the next
	// consumer instead of suppressing them (legacy behavior).
	PropagateErrorsGate = "processor.batch.propagateerrors"
)

var (
	// UseExporterHelper is the feature gate for using exporterhelper components.
	UseExporterHelper = featuregate.GlobalRegistry().MustRegister(
		UseExporterHelperGate,
		featuregate.StageBeta,
		featuregate.WithRegisterDescription("Use exporterhelper components for batching instead of legacy implementation"),
		featuregate.WithRegisterFromVersion("v0.131.0"),
	)

	// PropagateErrors is the feature gate for propagating errors instead of suppressing them.
	PropagateErrors = featuregate.GlobalRegistry().MustRegister(
		PropagateErrorsGate,
		featuregate.StageBeta,
		featuregate.WithRegisterDescription("Propagate errors from next consumer instead of suppressing them"),
		featuregate.WithRegisterFromVersion("v0.131.0"),
	)
)
