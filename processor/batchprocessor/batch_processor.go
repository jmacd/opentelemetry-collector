// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package batchprocessor // import "go.opentelemetry.io/collector/processor/batchprocessor"

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"go.uber.org/zap"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
)

// batch_processor is a component that accepts spans and metrics, places them
// into batches and sends downstream.
//
// batch_processor implements consumer.Traces and consumer.Metrics
//
// Batches are sent out with any of the following conditions:
// - batch size reaches cfg.SendBatchSize
// - cfg.Timeout is elapsed since the timestamp when the previous batch was sent out.
type batchProcessor struct {
	logger           *zap.Logger
	exportCtx        context.Context
	timer            *time.Timer
	timeout          time.Duration
	sendBatchSize    int
	sendBatchMaxSize int
	backPressure     bool

	newItem chan chanItem
	batch   batch

	shutdownC  chan struct{}
	goroutines sync.WaitGroup

	telemetry *batchProcessorTelemetry
}

type chanItem struct {
	waiter chan error
	data   any
}

type batch interface {
	// export the current batch
	export(ctx context.Context, sendBatchMaxSize int, returnBytes bool) (sentBatchSize int, sentBatchBytes int, err error)

	// itemCount returns the size of the current batch
	itemCount() int

	// add item to the current batch
	add(item chanItem)

	// add a waiter
	addWaiter(ch <-chan error)
}

var _ consumer.Traces = (*batchProcessor)(nil)
var _ consumer.Metrics = (*batchProcessor)(nil)
var _ consumer.Logs = (*batchProcessor)(nil)

func newBatchProcessor(set processor.CreateSettings, cfg *Config, batch batch, useOtel bool) (*batchProcessor, error) {
	bpt, err := newBatchProcessorTelemetry(set, useOtel)
	if err != nil {
		return nil, fmt.Errorf("error to create batch processor telemetry %w", err)
	}

	return &batchProcessor{
		logger:    set.Logger,
		exportCtx: bpt.exportCtx,
		telemetry: bpt,

		sendBatchSize:    int(cfg.SendBatchSize),
		sendBatchMaxSize: int(cfg.SendBatchMaxSize),
		timeout:          cfg.Timeout,
		newItem:          make(chan chanItem, runtime.NumCPU()),
		batch:            batch,
		shutdownC:        make(chan struct{}, 1),
	}, nil
}

func (bp *batchProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

// Start is invoked during service startup.
func (bp *batchProcessor) Start(context.Context, component.Host) error {
	bp.goroutines.Add(1)
	go bp.startProcessingCycle()
	return nil
}

// Shutdown is invoked during service shutdown.
func (bp *batchProcessor) Shutdown(context.Context) error {
	close(bp.shutdownC)

	// Wait until all goroutines are done.
	bp.goroutines.Wait()
	return nil
}

func (bp *batchProcessor) startProcessingCycle() {
	defer bp.goroutines.Done()
	bp.timer = time.NewTimer(bp.timeout)
	for {
		select {
		case <-bp.shutdownC:
		DONE:
			for {
				select {
				case item := <-bp.newItem:
					bp.processItem(item)
				default:
					break DONE
				}
			}
			// This is the close of the channel
			if bp.batch.itemCount() > 0 {
				// TODO: Set a timeout on sendTraces or
				// make it cancellable using the context that Shutdown gets as a parameter
				bp.sendItems(triggerTimeout)
			}
			return
		case item := <-bp.newItem:
			bp.processItem(item)
		case <-bp.timer.C:
			if bp.batch.itemCount() > 0 {
				bp.sendItems(triggerTimeout)
			}
			bp.resetTimer()
		}
	}
}

func (bp *batchProcessor) processItem(item chanItem) {
	bp.batch.add(item)
	if item.waiter != nil {
		bp.batch.addWaiter(item.waiter)
	}
	sent := false
	for bp.batch.itemCount() >= bp.sendBatchSize {
		sent = true
		bp.sendItems(triggerBatchSize)
	}

	if sent {
		bp.stopTimer()
		bp.resetTimer()
	}
}

func (bp *batchProcessor) stopTimer() {
	if !bp.timer.Stop() {
		<-bp.timer.C
	}
}

func (bp *batchProcessor) resetTimer() {
	bp.timer.Reset(bp.timeout)
}

func (bp *batchProcessor) sendItems(trigger trigger) {
	sent, bytes, err := bp.batch.export(bp.exportCtx, bp.sendBatchMaxSize, bp.telemetry.detailed)
	if err != nil {
		bp.logger.Warn("Sender failed", zap.Error(err))
	} else {
		bp.telemetry.record(trigger, int64(sent), int64(bytes))
	}
}

func (bp *batchProcessor) consume(ctx context.Context, data any) error {
	item := chanItem{
		data: data,
	}
	if bp.backPressure {
		item.waiter = make(chan error)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case bp.newItem <- item:
		// sent!
		if !bp.backPressure {
			return nil
		}
	}

	select {
	case err := <-item.waiter:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ConsumeTraces implements TracesProcessor
func (bp *batchProcessor) ConsumeTraces(ctx context.Context, td ptrace.Traces) error {
	if td.SpanCount() == 0 {
		return nil
	}
	return bp.consume(ctx, td)
}

// ConsumeMetrics implements MetricsProcessor
func (bp *batchProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	if md.DataPointCount() == 0 {
		return nil
	}
	return bp.consume(ctx, md)
}

// ConsumeLogs implements LogsProcessor
func (bp *batchProcessor) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	if ld.LogRecordCount() == 0 {
		return nil
	}
	return bp.consume(ctx, ld)
}

// newBatchTracesProcessor creates a new batch processor that batches traces by size or with timeout
func newBatchTracesProcessor(set processor.CreateSettings, next consumer.Traces, cfg *Config, useOtel bool) (*batchProcessor, error) {
	return newBatchProcessor(set, cfg, newBatchTraces(next), useOtel)
}

// newBatchMetricsProcessor creates a new batch processor that batches metrics by size or with timeout
func newBatchMetricsProcessor(set processor.CreateSettings, next consumer.Metrics, cfg *Config, useOtel bool) (*batchProcessor, error) {
	return newBatchProcessor(set, cfg, newBatchMetrics(next), useOtel)
}

// newBatchLogsProcessor creates a new batch processor that batches logs by size or with timeout
func newBatchLogsProcessor(set processor.CreateSettings, next consumer.Logs, cfg *Config, useOtel bool) (*batchProcessor, error) {
	return newBatchProcessor(set, cfg, newBatchLogs(next), useOtel)
}

type waiters []<-chan error

func (w *waiters) addWaiter(wch <-chan error) {
	*w = append(*w, wch)
}

type batchTraces struct {
	nextConsumer consumer.Traces
	traceData    ptrace.Traces
	spanCount    int
	sizer        ptrace.Sizer
	waiters
}

func newBatchTraces(nextConsumer consumer.Traces) *batchTraces {
	return &batchTraces{nextConsumer: nextConsumer, traceData: ptrace.NewTraces(), sizer: &ptrace.ProtoMarshaler{}}
}

// add updates current batchTraces by adding new TraceData object
func (bt *batchTraces) add(item chanItem) {
	td := item.data.(ptrace.Traces)
	bt.spanCount += td.SpanCount()
	td.ResourceSpans().MoveAndAppendTo(bt.traceData.ResourceSpans())
}

func (bt *batchTraces) export(ctx context.Context, sendBatchMaxSize int, returnBytes bool) (int, int, error) {
	var req ptrace.Traces
	var sent int
	var bytes int
	if sendBatchMaxSize > 0 && bt.itemCount() > sendBatchMaxSize {
		req = splitTraces(sendBatchMaxSize, bt.traceData)
		bt.spanCount -= sendBatchMaxSize
		sent = sendBatchMaxSize
	} else {
		req = bt.traceData
		sent = bt.spanCount
		bt.traceData = ptrace.NewTraces()
		bt.spanCount = 0
	}
	if returnBytes {
		bytes = bt.sizer.TracesSize(req)
	}
	return sent, bytes, bt.nextConsumer.ConsumeTraces(ctx, req)
}

func (bt *batchTraces) itemCount() int {
	return bt.spanCount
}

type batchMetrics struct {
	nextConsumer   consumer.Metrics
	metricData     pmetric.Metrics
	dataPointCount int
	sizer          pmetric.Sizer
	waiters
}

func newBatchMetrics(nextConsumer consumer.Metrics) *batchMetrics {
	return &batchMetrics{nextConsumer: nextConsumer, metricData: pmetric.NewMetrics(), sizer: &pmetric.ProtoMarshaler{}}
}

func (bm *batchMetrics) export(ctx context.Context, sendBatchMaxSize int, returnBytes bool) (int, int, error) {
	var req pmetric.Metrics
	var sent int
	var bytes int
	if sendBatchMaxSize > 0 && bm.dataPointCount > sendBatchMaxSize {
		req = splitMetrics(sendBatchMaxSize, bm.metricData)
		bm.dataPointCount -= sendBatchMaxSize
		sent = sendBatchMaxSize
	} else {
		req = bm.metricData
		sent = bm.dataPointCount
		bm.metricData = pmetric.NewMetrics()
		bm.dataPointCount = 0
	}
	if returnBytes {
		bytes = bm.sizer.MetricsSize(req)
	}
	return sent, bytes, bm.nextConsumer.ConsumeMetrics(ctx, req)
}

func (bm *batchMetrics) itemCount() int {
	return bm.dataPointCount
}

func (bm *batchMetrics) add(item chanItem) {
	md := item.data.(pmetric.Metrics)
	bm.dataPointCount += md.DataPointCount()
	md.ResourceMetrics().MoveAndAppendTo(bm.metricData.ResourceMetrics())
}

type batchLogs struct {
	nextConsumer consumer.Logs
	logData      plog.Logs
	logCount     int
	sizer        plog.Sizer
	waiters
}

func newBatchLogs(nextConsumer consumer.Logs) *batchLogs {
	return &batchLogs{nextConsumer: nextConsumer, logData: plog.NewLogs(), sizer: &plog.ProtoMarshaler{}}
}

func (bl *batchLogs) export(ctx context.Context, sendBatchMaxSize int, returnBytes bool) (int, int, error) {
	var req plog.Logs
	var sent int
	var bytes int
	if sendBatchMaxSize > 0 && bl.logCount > sendBatchMaxSize {
		req = splitLogs(sendBatchMaxSize, bl.logData)
		bl.logCount -= sendBatchMaxSize
		sent = sendBatchMaxSize
	} else {
		req = bl.logData
		sent = bl.logCount
		bl.logData = plog.NewLogs()
		bl.logCount = 0
	}
	if returnBytes {
		bytes = bl.sizer.LogsSize(req)
	}
	return sent, bytes, bl.nextConsumer.ConsumeLogs(ctx, req)
}

func (bl *batchLogs) itemCount() int {
	return bl.logCount
}

func (bl *batchLogs) add(item chanItem) {
	ld := item.data.(plog.Logs)
	bl.logCount += ld.LogRecordCount()
	ld.ResourceLogs().MoveAndAppendTo(bl.logData.ResourceLogs())
}
