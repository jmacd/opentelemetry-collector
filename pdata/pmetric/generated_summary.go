// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Code generated by "pdata/internal/cmd/pdatagen/main.go". DO NOT EDIT.
// To regenerate this file run "make genpdata".

package pmetric

import (
	"go.opentelemetry.io/collector/pdata/internal"
	otlpmetrics "go.opentelemetry.io/collector/pdata/internal/data/protogen/metrics/v1"
)

// Summary represents the type of a metric that is calculated by aggregating as a Summary of all reported double measurements over a time interval.
//
// This is a reference type, if passed by value and callee modifies it the
// caller will see the modification.
//
// Must use NewSummary function to create new instances.
// Important: zero-initialized instance is not valid for use.
type Summary struct {
	orig  *otlpmetrics.Summary
	state *internal.State
}

func newSummary(orig *otlpmetrics.Summary, state *internal.State) Summary {
	return Summary{orig: orig, state: state}
}

// NewSummary creates a new empty Summary.
//
// This must be used only in testing code. Users should use "AppendEmpty" when part of a Slice,
// OR directly access the member if this is embedded in another struct.
func NewSummary() Summary {
	state := internal.StateMutable
	return newSummary(&otlpmetrics.Summary{}, &state)
}

// MoveTo moves all properties from the current struct overriding the destination and
// resetting the current instance to its zero value
func (ms Summary) MoveTo(dest Summary) {
	ms.state.AssertMutable()
	dest.state.AssertMutable()
	*dest.orig = *ms.orig
	*ms.orig = otlpmetrics.Summary{}
}

// DataPoints returns the DataPoints associated with this Summary.
func (ms Summary) DataPoints() SummaryDataPointSlice {
	return newSummaryDataPointSlice(&ms.orig.DataPoints, ms.state)
}

// CopyTo copies all properties from the current struct overriding the destination.
func (ms Summary) CopyTo(dest Summary) {
	dest.state.AssertMutable()
	ms.DataPoints().CopyTo(dest.DataPoints())
}

// ValidateUTF8 ensures all contents have a valid UTF8 encoding.
func (ms Summary) ValidateUTF8(repl string) {
	ms.DataPoints().ValidateUTF8(repl)
}
