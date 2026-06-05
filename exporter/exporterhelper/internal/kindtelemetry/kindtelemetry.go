// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package kindtelemetry encapsulates the per-component-kind identity used by
// exporterhelper's observability code: metric names, metric attributes, and
// span name prefixes.
//
// The default identity is the Exporter kind, matching the names recorded in
// exporterhelper/metadata.yaml: "otelcol_exporter_*" metric names, the
// "exporter" attribute, and span names of the form "exporter/...".
// Selecting the Processor kind rewrites the "otelcol_exporter_" metric name
// prefix to "otelcol_processor_", changes the attribute key to "processor",
// and uses "processor" in span names.
//
// This package exists to support reusing exporterhelper's QueueBatch logic
// from a processor without leaking exporter-shaped telemetry into processor
// pipelines. See
// https://github.com/open-telemetry/opentelemetry-collector/issues/14038.
package kindtelemetry // import "go.opentelemetry.io/collector/exporter/exporterhelper/internal/kindtelemetry"

import (
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal/metadata"
)

const (
	exporterAttr  = "exporter"
	processorAttr = "processor"

	metricExporterPrefix  = "otelcol_exporter_"
	metricProcessorPrefix = "otelcol_processor_"
)

// Identity captures the component-kind-dependent inputs to exporterhelper's
// observability code. The zero value is the Exporter identity.
type Identity struct {
	// AttributeKey is the attribute key used to identify the component in
	// metric attributes and trace span attributes, such as "exporter" or
	// "processor".
	AttributeKey string
	// SpanNamespace is used as the first segment of span names emitted by
	// exporterhelper, such as the "exporter" in "exporter/<id>/<signal>".
	SpanNamespace string
	// metricOldPrefix and metricNewPrefix, when both non-empty, are passed to
	// the generated metadata.WithMetricNamePrefixReplacement option so the
	// TelemetryBuilder emits metric names appropriate for the component kind.
	metricOldPrefix string
	metricNewPrefix string
}

// Default returns the Identity used by an Exporter, which is also
// exporterhelper's default identity.
func Default() Identity {
	return Identity{
		AttributeKey:  exporterAttr,
		SpanNamespace: exporterAttr,
	}
}

// ForKind returns the Identity that exporterhelper observability code should
// use when the surrounding component is of the given kind. The zero-value
// Kind and KindExporter both return the Default identity; KindProcessor
// returns the processor-shaped identity. Any other kind returns an error.
func ForKind(kind component.Kind) (Identity, error) {
	switch kind {
	case (component.Kind{}), component.KindExporter:
		return Default(), nil
	case component.KindProcessor:
		return Identity{
			AttributeKey:    processorAttr,
			SpanNamespace:   processorAttr,
			metricOldPrefix: metricExporterPrefix,
			metricNewPrefix: metricProcessorPrefix,
		}, nil
	default:
		return Identity{}, fmt.Errorf("kindtelemetry: unsupported component kind %q; only Exporter and Processor are supported", kind.String())
	}
}

// Resolve returns the Identity for kind, substituting Default() when kind is
// the zero value, KindExporter, or any other value. It is intended for
// settings consumers that have already validated kind earlier in the call
// chain via ForKind.
func Resolve(kind component.Kind) Identity {
	id, err := ForKind(kind)
	if err != nil {
		return Default()
	}
	return id
}

// TelemetryBuilderOptions returns the metadata.TelemetryBuilderOption values
// required for the generated TelemetryBuilder to produce metric names
// appropriate for this Identity. The returned slice is empty when no
// substitution is required.
func (i Identity) TelemetryBuilderOptions() []metadata.TelemetryBuilderOption {
	if i.metricOldPrefix == "" || i.metricNewPrefix == "" {
		return nil
	}
	return []metadata.TelemetryBuilderOption{
		metadata.WithMetricNamePrefixReplacement(i.metricOldPrefix, i.metricNewPrefix),
	}
}
