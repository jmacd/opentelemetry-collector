// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package exporterhelper // import "go.opentelemetry.io/collector/exporter/exporterhelper"

import (
	"go.opentelemetry.io/otel/attribute"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configretry"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter/exporterhelper/internal"
)

// Option apply changes to BaseExporter.
type Option = internal.Option

// WithStart overrides the default Start function for an exporter.
// The default start function does nothing and always returns nil.
func WithStart(start component.StartFunc) Option {
	return internal.WithStart(start)
}

// WithShutdown overrides the default Shutdown function for an exporter.
// The default shutdown function does nothing and always returns nil.
func WithShutdown(shutdown component.ShutdownFunc) Option {
	return internal.WithShutdown(shutdown)
}

// WithTimeout overrides the default TimeoutConfig for an exporter.
// The default TimeoutConfig is 5 seconds.
func WithTimeout(timeoutConfig TimeoutConfig) Option {
	return internal.WithTimeout(timeoutConfig)
}

// WithRetry overrides the default configretry.BackOffConfig for an exporter.
// The default configretry.BackOffConfig is to disable retries.
func WithRetry(config configretry.BackOffConfig) Option {
	return internal.WithRetry(config)
}

// WithCapabilities overrides the default Capabilities() function for a Consumer.
// The default is non-mutable data.
// TODO: Verify if we can change the default to be mutable as we do for processors.
func WithCapabilities(capabilities consumer.Capabilities) Option {
	return internal.WithCapabilities(capabilities)
}

// WithAttrs adds extra attributes to the metrics produced by the exporter
// The default set of extra attribute is empty
func WithAttrs(attrs ...attribute.KeyValue) Option {
	return internal.WithAttributes(attrs...)
}

// WithTelemetryComponentKind selects the component kind under which this
// exporterhelper instance reports telemetry. The default is
// component.KindExporter, which preserves the historical
// "otelcol_exporter_*" metric names, the "exporter" metric attribute, and
// the "exporter/<id>/<signal>" span names. Passing
// component.KindProcessor rewrites those to "otelcol_processor_*",
// "processor", and "processor/<id>/<signal>" respectively. Only the
// zero-value Kind, KindExporter, and KindProcessor are accepted; any other
// Kind value causes the option to return an error.
//
// This option exists to support reusing exporterhelper's queue and batch
// logic from a processor. See
// https://github.com/open-telemetry/opentelemetry-collector/issues/14038.
func WithTelemetryComponentKind(kind component.Kind) Option {
	return internal.WithTelemetryComponentKind(kind)
}
