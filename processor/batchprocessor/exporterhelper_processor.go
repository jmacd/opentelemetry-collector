// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package batchprocessor // import "go.opentelemetry.io/collector/processor/batchprocessor"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configoptional"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/batchprocessor/internal"
)

// translateToExporterHelperConfig converts legacy batchprocessor config to exporterhelper config
func translateToExporterHelperConfig(cfg *Config) exporterhelper.QueueBatchConfig {
	// These settings match legacy behavior
	queueBatchConfig := exporterhelper.QueueBatchConfig{
		Enabled:         true,
		WaitForResult:   true, 
		BlockOnOverflow: true,
		Sizer:           exporterhelper.RequestSizerTypeItems,
		QueueSize:       int64(max(cfg.SendBatchSize, cfg.SendBatchMaxSize, 1000)),
		NumConsumers:    1,
	}

	if cfg.SendBatchSize > 0 || cfg.SendBatchMaxSize > 0 || cfg.Timeout > 0 {
		batchConfig := exporterhelper.BatchConfig{
			FlushTimeout: cfg.Timeout,
			Sizer:        exporterhelper.RequestSizerTypeItems,
			MinSize:      0, // Default: no minimum
			MaxSize:      0, // Default: no maximum
		}

		// Map send_batch_size to MinSize (minimum items to trigger batch)
		if cfg.SendBatchSize > 0 {
			batchConfig.MinSize = int64(cfg.SendBatchSize)
		}

		// Map send_batch_max_size to MaxSize (maximum items in batch)
		if cfg.SendBatchMaxSize > 0 {
			batchConfig.MaxSize = int64(cfg.SendBatchMaxSize)
		}

		queueBatchConfig.Batch = configoptional.Some(batchConfig)
	}

	return queueBatchConfig
}

// newTracesProcessorWithExporterHelper creates a new traces processor using exporterhelper components.
func newTracesProcessorWithExporterHelper(set processor.Settings, nextConsumer consumer.Traces, cfg *Config) (processor.Traces, error) {
	// Translate legacy config to exporterhelper config
	queueBatchConfig := translateToExporterHelperConfig(cfg)

	// Create a bridge exporter that wraps the next consumer
	exporterSet := exporter.Settings{
		ID:                set.ID,
		TelemetrySettings: set.TelemetrySettings,
		BuildInfo:         set.BuildInfo,
	}

	// Create an exporter that pushes to the next consumer
	tracesExporter, err := exporterhelper.NewTraces(
		context.Background(),
		exporterSet,
		&bridgeConfig{},
		func(ctx context.Context, traces ptrace.Traces) error {
			if internal.PropagateErrors.IsEnabled() {
				return nextConsumer.ConsumeTraces(ctx, traces)
			}
			// Legacy behavior: suppress errors
			_ = nextConsumer.ConsumeTraces(ctx, traces)
			return nil
		},
		exporterhelper.WithQueue(queueBatchConfig),
	)
	if err != nil {
		return nil, err
	}

	// Create a processor wrapper
	return &tracesProcessorWrapper{
		exporter: tracesExporter,
	}, nil
}

// newMetricsProcessorWithExporterHelper creates a new metrics processor using exporterhelper components.
func newMetricsProcessorWithExporterHelper(set processor.Settings, nextConsumer consumer.Metrics, cfg *Config) (processor.Metrics, error) {
	// Translate legacy config to exporterhelper config
	queueBatchConfig := translateToExporterHelperConfig(cfg)

	// Create a bridge exporter that wraps the next consumer
	exporterSet := exporter.Settings{
		ID:                set.ID,
		TelemetrySettings: set.TelemetrySettings,
		BuildInfo:         set.BuildInfo,
	}

	// Create an exporter that pushes to the next consumer
	metricsExporter, err := exporterhelper.NewMetrics(
		context.Background(),
		exporterSet,
		&bridgeConfig{},
		func(ctx context.Context, metrics pmetric.Metrics) error {
			if internal.PropagateErrors.IsEnabled() {
				return nextConsumer.ConsumeMetrics(ctx, metrics)
			}
			// Legacy behavior: suppress errors
			_ = nextConsumer.ConsumeMetrics(ctx, metrics)
			return nil
		},
		exporterhelper.WithQueue(queueBatchConfig),
	)
	if err != nil {
		return nil, err
	}

	// Create a processor wrapper
	return &metricsProcessorWrapper{
		exporter: metricsExporter,
	}, nil
}

// newLogsProcessorWithExporterHelper creates a new logs processor using exporterhelper components.
func newLogsProcessorWithExporterHelper(set processor.Settings, nextConsumer consumer.Logs, cfg *Config) (processor.Logs, error) {
	// Translate legacy config to exporterhelper config
	queueBatchConfig := translateToExporterHelperConfig(cfg)

	// Create a bridge exporter that wraps the next consumer
	exporterSet := exporter.Settings{
		ID:                set.ID,
		TelemetrySettings: set.TelemetrySettings,
		BuildInfo:         set.BuildInfo,
	}

	// Create an exporter that pushes to the next consumer
	logsExporter, err := exporterhelper.NewLogs(
		context.Background(),
		exporterSet,
		&bridgeConfig{},
		func(ctx context.Context, logs plog.Logs) error {
			if internal.PropagateErrors.IsEnabled() {
				return nextConsumer.ConsumeLogs(ctx, logs)
			}
			// Legacy behavior: suppress errors
			_ = nextConsumer.ConsumeLogs(ctx, logs)
			return nil
		},
		exporterhelper.WithQueue(queueBatchConfig),
	)
	if err != nil {
		return nil, err
	}

	// Create a processor wrapper
	return &logsProcessorWrapper{
		exporter: logsExporter,
	}, nil
}

// bridgeConfig is a minimal config for the bridge exporter
type bridgeConfig struct{}

func (bc *bridgeConfig) Validate() error {
	return nil
}

// Processor wrappers that implement the processor interfaces

type tracesProcessorWrapper struct {
	exporter exporter.Traces
}

func (tpw *tracesProcessorWrapper) ConsumeTraces(ctx context.Context, traces ptrace.Traces) error {
	return tpw.exporter.ConsumeTraces(ctx, traces)
}

func (tpw *tracesProcessorWrapper) Capabilities() consumer.Capabilities {
	return tpw.exporter.Capabilities()
}

func (tpw *tracesProcessorWrapper) Start(ctx context.Context, host component.Host) error {
	return tpw.exporter.Start(ctx, host)
}

func (tpw *tracesProcessorWrapper) Shutdown(ctx context.Context) error {
	return tpw.exporter.Shutdown(ctx)
}

type metricsProcessorWrapper struct {
	exporter exporter.Metrics
}

func (mpw *metricsProcessorWrapper) ConsumeMetrics(ctx context.Context, metrics pmetric.Metrics) error {
	return mpw.exporter.ConsumeMetrics(ctx, metrics)
}

func (mpw *metricsProcessorWrapper) Capabilities() consumer.Capabilities {
	return mpw.exporter.Capabilities()
}

func (mpw *metricsProcessorWrapper) Start(ctx context.Context, host component.Host) error {
	return mpw.exporter.Start(ctx, host)
}

func (mpw *metricsProcessorWrapper) Shutdown(ctx context.Context) error {
	return mpw.exporter.Shutdown(ctx)
}

type logsProcessorWrapper struct {
	exporter exporter.Logs
}

func (lpw *logsProcessorWrapper) ConsumeLogs(ctx context.Context, logs plog.Logs) error {
	return lpw.exporter.ConsumeLogs(ctx, logs)
}

func (lpw *logsProcessorWrapper) Capabilities() consumer.Capabilities {
	return lpw.exporter.Capabilities()
}

func (lpw *logsProcessorWrapper) Start(ctx context.Context, host component.Host) error {
	return lpw.exporter.Start(ctx, host)
}

func (lpw *logsProcessorWrapper) Shutdown(ctx context.Context) error {
	return lpw.exporter.Shutdown(ctx)
}
