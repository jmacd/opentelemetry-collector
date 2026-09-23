// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package payload

import "errors"

// LogRange identifies a half-open range of records in the failed request.
type LogRange struct{ Start, End int }

// PartialError identifies exactly which records may be retried. It owns no
// payload references; the retry sender selects these ranges from its live input.
type PartialError struct {
	Err   error
	Retry []LogRange
}

func (e *PartialError) Error() string { return e.Err.Error() }
func (e *PartialError) Unwrap() error { return e.Err }

// RetrySubset validates nonoverlapping, ordered retry ranges and returns an
// independently owned native subset, without object conversion.
func (p *Payload) RetrySubset(ranges []LogRange) (*Payload, error) {
	if len(ranges) == 0 {
		return nil, errors.New("partial retry requires at least one range")
	}
	var parts []*Payload
	defer func() {
		for _, part := range parts {
			part.Release()
		}
	}()
	end := 0
	for _, interval := range ranges {
		if interval.Start < end || interval.End <= interval.Start {
			return nil, errors.New("partial retry ranges must be ordered and nonoverlapping")
		}
		part, err := p.Slice(interval.Start, interval.End)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		end = interval.End
	}
	return p.registry.Merge(parts...)
}
