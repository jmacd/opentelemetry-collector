// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package payload experiments with immutable, reference-counted pipeline payloads.
// It deliberately has no dependency on pdata, protobuf, or Arrow.
package payload

import (
	"errors"
	"fmt"
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
	ItemsCount() int
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
	r := &Registry{canonical: canonical.Format, codecs: map[Format]Codec{canonical.Format: canonical}}
	for _, c := range codecs {
		if c.Format == "" || c.Decode == nil || c.Encode == nil {
			return nil, fmt.Errorf("incomplete codec for %q", c.Format)
		}
		if _, exists := r.codecs[c.Format]; exists {
			return nil, fmt.Errorf("duplicate codec for %q", c.Format)
		}
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
	if data.ItemsCount() < 0 {
		return nil, errors.New("negative item count")
	}
	return &Payload{
		registry: r, format: data.Format(), items: data.ItemsCount(), refs: 1,
		cache: map[Format]result{data.Format(): {data: data}},
	}, nil
}

func (p *Payload) Format() Format  { return p.format }
func (p *Payload) ItemsCount() int { return p.items }

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
	if format == p.registry.canonical {
		data, err = p.registry.codecs[p.format].Decode(p.cache[p.format].data)
	} else {
		data, err = p.asLocked(p.registry.canonical)
		if err == nil {
			data, err = p.registry.codecs[format].Encode(data)
		}
	}
	if err == nil {
		err = validate(data, format, p.items)
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
	if data == nil || data.Format() != format || data.ItemsCount() != items {
		return fmt.Errorf("codec must preserve format %q and item count %d", format, items)
	}
	return nil
}

// Merge batches same-format payloads without converting them. Inputs remain
// owned by the caller. Hard-limit splitting and mixed formats are not supported.
func (r *Registry) Merge(inputs ...*Payload) (*Payload, error) {
	if len(inputs) == 0 || inputs[0] == nil {
		return nil, errors.New("merge requires non-nil inputs")
	}
	format := inputs[0].Format()
	codec, ok := r.codecs[format]
	if !ok || codec.Merge == nil {
		return nil, fmt.Errorf("merge unsupported for %q", format)
	}
	data := make([]Representation, 0, len(inputs))
	items := 0
	for _, input := range inputs {
		if input == nil || input.registry != r || input.Format() != format {
			return nil, errors.New("merge requires matching registries and formats")
		}
		if input.ItemsCount() > math.MaxInt-items {
			return nil, errors.New("merged item count overflows int")
		}
		items += input.ItemsCount()
		rep, err := input.As(format)
		if err != nil {
			return nil, err
		}
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
