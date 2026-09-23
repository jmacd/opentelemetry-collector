// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package payload

import (
	"errors"
	"fmt"
)

type Sizer int

const (
	SizerItems Sizer = iota
	SizerBytes
)

// MergeSplit returns independently owned native payloads. Failure is atomic:
// inputs remain untouched and no partial output is returned. A single item
// exceeding a byte limit is reported as an error, never silently dropped.
func (p *Payload) MergeSplit(maxSize int, sizer Sizer, other *Payload) (out []*Payload, err error) {
	if maxSize < 0 || (sizer != SizerItems && sizer != SizerBytes) {
		return nil, errors.New("invalid batch limit or sizer")
	}
	inputs := []*Payload{p}
	if other != nil {
		inputs = append(inputs, other)
	}
	merged, err := p.registry.Merge(inputs...)
	if err != nil {
		return nil, err
	}
	defer merged.Release()
	sizeOf := func(part *Payload) (int, error) {
		if sizer == SizerItems {
			return part.ItemsCount()
		}
		return part.BytesSize()
	}
	size, err := sizeOf(merged)
	if err != nil {
		return nil, err
	}
	if maxSize == 0 || size <= maxSize {
		if retainErr := merged.Retain(); retainErr != nil {
			return nil, retainErr
		}
		return []*Payload{merged}, nil
	}
	count, err := merged.ItemsCount()
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, errors.New("empty batch metadata exceeds byte limit")
	}
	defer func() {
		if err != nil {
			for _, part := range out {
				part.Release()
			}
			out = nil
		}
	}()
	var sizes []int
	for start := 0; start < count; {
		low, high := 1, count-start
		if sizer == SizerItems {
			low, high = min(maxSize, high), min(maxSize, high)
		}
		var best *Payload
		bestSize, bestCount := 0, 0
		for low <= high {
			n := low + (high-low)/2
			candidate, sliceErr := merged.Slice(start, start+n)
			if sliceErr != nil {
				if best != nil {
					best.Release()
				}
				return out, sliceErr
			}
			measured, sizeErr := sizeOf(candidate)
			if sizeErr != nil {
				candidate.Release()
				if best != nil {
					best.Release()
				}
				return out, sizeErr
			}
			if measured <= maxSize {
				if best != nil {
					best.Release()
				}
				best, bestCount, bestSize = candidate, n, measured
				low = n + 1
			} else {
				candidate.Release()
				high = n - 1
			}
		}
		if best == nil {
			return out, fmt.Errorf("log record %d exceeds batch byte limit %d", start, maxSize)
		}
		out = append(out, best)
		sizes = append(sizes, bestSize)
		start += bestCount
	}
	// Exporterhelper keeps the smallest remainder as its pending batch.
	smallest := len(sizes) - 1
	for i := range sizes {
		if sizes[i] < sizes[smallest] {
			smallest = i
		}
	}
	remainder := out[smallest]
	copy(out[smallest:], out[smallest+1:])
	out[len(out)-1] = remainder
	return out, nil
}
