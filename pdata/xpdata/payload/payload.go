// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package payload experiments with immutable, reference-counted pipeline payloads.
// It deliberately has no dependency on pdata, protobuf, or Arrow.
package payload

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"sync"
)

// Format identifies a signal, representation, and schema version.
type Format string

// Representation owns immutable data. Release relinquishes its ownership.
// Implementations must not return a typed nil or alias another representation's
// ownership without first retaining the underlying data.
type Representation interface {
	Format() Format
	ItemsCount() (int, error)
	Release()
}

type (
	ConvertFunc func(Representation) (Representation, error)
	MergeFunc   func([]Representation) (Representation, error)
)

// Codec converts to/from the registry's canonical object representation.
// Merge is optional and must preserve inputs, item counts, and format.
// Conversion/merge functions own their output only on success and must clean up
// partial results on error. They must support concurrent calls.
// Converters must be deterministic and perform no I/O: failures are cached.
type Codec struct {
	Format Format
	Decode ConvertFunc
	Encode ConvertFunc
	Merge  MergeFunc
	View   func(Representation) (LogsView, error)
	Slice  func(Representation, int, int) (Representation, error)
	Size   func(Representation) (int, error)
	Direct map[Format]ConvertFunc
	// MergeFormat on the canonical codec selects the common representation
	// for mixed-format batching. Homogeneous batches retain their format.
	MergeFormat Format
}

// Registry is immutable after construction. No process-global registration is used.
type Registry struct {
	canonical Format
	codecs    map[Format]Codec
}

func NewRegistry(canonical Codec, codecs ...Codec) (*Registry, error) {
	if canonical.Format == "" {
		return nil, errors.New("canonical format is empty")
	}
	canonical.Direct = maps.Clone(canonical.Direct)
	r := &Registry{canonical: canonical.Format, codecs: map[Format]Codec{canonical.Format: canonical}}
	for _, c := range codecs {
		if c.Format == "" || c.Decode == nil || c.Encode == nil {
			return nil, fmt.Errorf("incomplete codec for %q", c.Format)
		}
		if _, exists := r.codecs[c.Format]; exists {
			return nil, fmt.Errorf("duplicate codec for %q", c.Format)
		}
		c.Direct = maps.Clone(c.Direct)
		r.codecs[c.Format] = c
	}
	return r, nil
}

type result struct {
	data Representation
	err  error
}

// Payload owns the original representation and lazily cached conversions.
// Retain before asynchronous handoff/fan-out and Release once per owner.
// As returns a borrowed, immutable representation valid while the caller owns
// a reference. Concurrent reads/conversions are safe; a caller must not release
// its last reference while using a borrowed representation.
type Payload struct {
	registry *Registry
	format   Format
	items    int
	counted  bool
	countErr error
	mu       sync.Mutex
	refs     int
	cache    map[Format]result
}

// New transfers ownership of data only on success.
func (r *Registry) New(data Representation) (*Payload, error) {
	if data == nil {
		return nil, errors.New("nil representation")
	}
	if _, ok := r.codecs[data.Format()]; !ok {
		return nil, fmt.Errorf("unregistered format %q", data.Format())
	}
	return &Payload{
		registry: r, format: data.Format(), refs: 1,
		cache: map[Format]result{data.Format(): {data: data}},
	}, nil
}

func (p *Payload) Format() Format { return p.format }

// Registry returns the immutable codec set, for wrapping a legacy processor's
// replacement or partial-retry objects with the same conversion capabilities.
func (p *Payload) Registry() *Registry { return p.registry }

// ItemsCount is fallible until a native view or object decoder establishes it.
// The object API's LogRecordCount remains infallible.
func (p *Payload) ItemsCount() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs == 0 {
		return 0, errors.New("access to released payload")
	}
	return p.countLocked()
}

func (p *Payload) countLocked() (int, error) {
	if !p.counted {
		source := p.cache[p.format].data
		if canonical := p.cache[p.registry.canonical].data; canonical != nil {
			source = canonical
		}
		p.items, p.countErr = source.ItemsCount()
		if p.countErr == nil && p.items < 0 {
			p.countErr = errors.New("negative item count")
		}
		p.counted = true
	}
	return p.items, p.countErr
}

func (p *Payload) View() (LogsView, error) {
	rep, err := p.As(p.format)
	if err != nil {
		return nil, err
	}
	view := p.registry.codecs[p.format].View
	if view == nil {
		return nil, fmt.Errorf("native logs view unavailable for %q", p.format)
	}
	return view(rep)
}

// BytesSize measures the representation's logical OTLP wire size, not retained
// memory. Arrow adapters measure through views, without constructing objects.
func (p *Payload) BytesSize() (int, error) {
	rep, err := p.As(p.format)
	if err != nil {
		return 0, err
	}
	size := p.registry.codecs[p.format].Size
	if size == nil {
		return 0, fmt.Errorf("byte sizing unavailable for %q", p.format)
	}
	return size(rep)
}

func (p *Payload) Slice(start, end int) (*Payload, error) {
	count, err := p.ItemsCount()
	if err != nil {
		return nil, err
	}
	if start < 0 || end < start || end > count {
		return nil, errors.New("invalid log slice bounds")
	}
	rep, err := p.As(p.format)
	if err != nil {
		return nil, err
	}
	slice := p.registry.codecs[p.format].Slice
	if slice == nil {
		return nil, fmt.Errorf("native slicing unavailable for %q", p.format)
	}
	out, err := slice(rep, start, end)
	if err != nil {
		return nil, err
	}
	if err = validate(out, p.format, end-start); err != nil {
		if out != nil {
			out.Release()
		}
		return nil, err
	}
	return p.registry.New(out)
}

func (p *Payload) Retain() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs == 0 {
		return errors.New("retain of released payload")
	}
	p.refs++
	return nil
}

// Release panics on an ownership programming error, not on malformed input.
func (p *Payload) Release() {
	p.mu.Lock()
	if p.refs == 0 {
		p.mu.Unlock()
		panic("release of released payload")
	}
	p.refs--
	if p.refs != 0 {
		p.mu.Unlock()
		return
	}
	cache := p.cache
	p.cache = nil
	p.mu.Unlock()
	for _, entry := range cache {
		if entry.data != nil {
			entry.data.Release()
		}
	}
}

func (p *Payload) As(format Format) (Representation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refs == 0 {
		return nil, errors.New("access to released payload")
	}
	if _, ok := p.registry.codecs[format]; !ok {
		return nil, fmt.Errorf("unregistered format %q", format)
	}
	return p.asLocked(format)
}

func (p *Payload) asLocked(format Format) (Representation, error) {
	if entry, ok := p.cache[format]; ok {
		return entry.data, entry.err
	}
	var data Representation
	var err error
	if direct := p.registry.codecs[p.format].Direct[format]; direct != nil {
		data, err = direct(p.cache[p.format].data)
	} else if format == p.registry.canonical {
		data, err = p.registry.codecs[p.format].Decode(p.cache[p.format].data)
	} else {
		data, err = p.asLocked(p.registry.canonical)
		if err == nil {
			data, err = p.registry.codecs[format].Encode(data)
		}
	}
	if err == nil {
		expected, countErr := p.countLocked()
		if data == nil || data.Format() != format {
			err = fmt.Errorf("codec returned an invalid representation for %q", format)
		} else {
			actual, actualErr := data.ItemsCount()
			switch {
			case actualErr != nil:
				err = actualErr
			case actual < 0 || (countErr == nil && actual != expected):
				err = fmt.Errorf("codec changed item count from %d to %d", expected, actual)
			default:
				p.items, p.counted, p.countErr = actual, true, nil
			}
		}
		if err != nil && data != nil {
			data.Release()
		}
	}
	if err != nil {
		data = nil
		err = fmt.Errorf("convert %q to %q: %w", p.format, format, err)
	}
	p.cache[format] = result{data: data, err: err}
	return data, err
}

func validate(data Representation, format Format, items int) error {
	if data == nil || data.Format() != format {
		return fmt.Errorf("codec must preserve format %q and item count %d", format, items)
	}
	count, err := data.ItemsCount()
	if err != nil {
		return err
	}
	if count != items {
		return fmt.Errorf("codec changed item count from %d to %d", items, count)
	}
	return nil
}

// Merge batches homogeneous payloads natively. Mixed inputs use the canonical
// codec's MergeFormat policy and registered converters. Inputs remain owned by
// the caller; MergeSplit applies hard size limits to the result.
func (r *Registry) Merge(inputs ...*Payload) (*Payload, error) {
	if len(inputs) == 0 || inputs[0] == nil {
		return nil, errors.New("merge requires non-nil inputs")
	}
	format := inputs[0].Format()
	for _, input := range inputs {
		if input == nil {
			return nil, errors.New("merge requires non-nil inputs")
		}
		if input.Format() != format {
			format = r.codecs[r.canonical].MergeFormat
			break
		}
	}
	codec, ok := r.codecs[format]
	if !ok || codec.Merge == nil {
		return nil, fmt.Errorf("merge unsupported for %q", format)
	}
	data := make([]Representation, 0, len(inputs))
	items := 0
	for _, input := range inputs {
		if input == nil {
			return nil, errors.New("merge requires non-nil inputs")
		}
		rep, err := input.As(format)
		if err != nil {
			return nil, err
		}
		count, err := input.ItemsCount()
		if err != nil {
			return nil, err
		}
		if count > math.MaxInt-items {
			return nil, errors.New("merged item count overflows int")
		}
		items += count
		data = append(data, rep)
	}
	merged, err := codec.Merge(data)
	if err != nil {
		return nil, err
	}
	if err = validate(merged, format, items); err != nil {
		if merged != nil {
			merged.Release()
		}
		return nil, err
	}
	return r.New(merged)
}
