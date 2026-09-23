// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package payload

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

type testData struct {
	format   Format
	items    int
	releases atomic.Int32
}

type unknownCount struct{ *testData }

func (*unknownCount) ItemsCount() (int, error) { return 0, errors.New("requires object decoding") }

func TestCountBecomesKnownAfterDecoding(t *testing.T) {
	t.Parallel()
	obj := &testData{format: "objects", items: 9}
	reg, err := NewRegistry(Codec{Format: "objects"}, Codec{
		Format: "deferred",
		Decode: func(Representation) (Representation, error) { return obj, nil },
		Encode: func(Representation) (Representation, error) { return nil, errors.New("unused") },
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := reg.New(&unknownCount{testData: &testData{format: "deferred"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release()
	if _, err := p.ItemsCount(); err == nil {
		t.Fatal("predecoded count must report its failure")
	}
	if _, err := p.As("objects"); err != nil {
		t.Fatal(err)
	}
	if n, err := p.ItemsCount(); err != nil || n != 9 {
		t.Fatalf("post-decode count = %d, %v", n, err)
	}
}

func (d *testData) Format() Format           { return d.format }
func (d *testData) ItemsCount() (int, error) { return d.items, nil }
func (d *testData) Release()                 { d.releases.Add(1) }

func TestLazyConversionAndOwnership(t *testing.T) {
	t.Parallel()
	var decodes, encodes atomic.Int32
	obj := &testData{format: "objects", items: 3}
	output := &testData{format: "output", items: 3}
	source := &testData{format: "input", items: 3}
	r, err := NewRegistry(Codec{Format: "objects"},
		Codec{Format: "input", Decode: func(Representation) (Representation, error) {
			decodes.Add(1)
			return obj, nil
		}, Encode: func(Representation) (Representation, error) { return nil, errors.New("unused") }},
		Codec{
			Format: "output", Decode: func(Representation) (Representation, error) { return nil, errors.New("unused") },
			Encode: func(Representation) (Representation, error) {
				encodes.Add(1)
				return output, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.New(source)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := p.As("input")
	if err != nil || rep != source || decodes.Load() != 0 {
		t.Fatalf("pass-through decoded: %v", err)
	}
	var wg sync.WaitGroup
	for range 16 {
		if err := p.Retain(); err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			defer p.Release()
			rep, err := p.As("output")
			if err != nil || rep != output {
				t.Errorf("convert: %v", err)
			}
		})
	}
	wg.Wait()
	if decodes.Load() != 1 || encodes.Load() != 1 {
		t.Fatal("conversion was not cached")
	}
	if source.releases.Load() != 0 {
		t.Fatal("released before last owner")
	}
	p.Release()
	for _, data := range []*testData{source, obj, output} {
		if data.releases.Load() != 1 {
			t.Fatal("each cached representation must be released exactly once")
		}
	}
	if err := p.Retain(); err == nil {
		t.Fatal("retained a released payload")
	}
	if _, err := p.As("input"); err == nil {
		t.Fatal("accessed a released payload")
	}
}

func TestCodecErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []string{"decode", "count", "format", "nil"} {
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			calls := 0
			bad := &testData{format: "objects", items: 1}
			sentinel := errors.New("decode failed")
			r, err := NewRegistry(Codec{Format: "objects"}, Codec{
				Format: "bytes",
				Encode: func(Representation) (Representation, error) { return nil, sentinel },
				Decode: func(Representation) (Representation, error) {
					calls++
					switch tc {
					case "decode":
						return nil, sentinel
					case "count":
						bad.items = 2
					case "format":
						bad.format = "wrong"
					case "nil":
						return nil, nil
					}
					return bad, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			p, err := r.New(&testData{format: "bytes", items: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Release()
			for range 2 {
				_, err := p.As("objects")
				if err == nil {
					t.Fatal("conversion error hidden")
				}
				if tc == "decode" && !errors.Is(err, sentinel) {
					t.Fatal("lost underlying error")
				}
			}
			if calls != 1 {
				t.Fatal("deterministic error not cached")
			}
			if (tc == "count" || tc == "format") && bad.releases.Load() != 1 {
				t.Fatal("invalid conversion result leaked")
			}
			if _, err := p.As("unknown"); err == nil {
				t.Fatal("unknown format accepted")
			}
		})
	}
}

func TestRegistrationAndMergeErrors(t *testing.T) {
	t.Parallel()
	convert := func(Representation) (Representation, error) { return nil, errors.New("unused") }
	for _, codecs := range [][]Codec{
		{{Format: "objects", Decode: convert, Encode: convert}},
		{{Format: "bytes", Decode: convert}},
		{{Decode: convert, Encode: convert}},
	} {
		if _, err := NewRegistry(Codec{Format: "objects"}, codecs...); err == nil {
			t.Fatal("invalid codec registration accepted")
		}
	}
	if _, err := NewRegistry(Codec{}); err == nil {
		t.Fatal("empty canonical format accepted")
	}
	r, err := NewRegistry(Codec{Format: "objects", Merge: func([]Representation) (Representation, error) {
		return &testData{format: "objects"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []Representation{nil, &testData{format: "unknown"}} {
		if _, newErr := r.New(data); newErr == nil {
			t.Fatal("invalid representation accepted")
		}
	}
	p, err := r.New(&testData{format: "objects", items: math.MaxInt})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release()
	for _, inputs := range [][]*Payload{nil, {nil}, {p, nil}, {p, p}, {p}} {
		if _, err := r.Merge(inputs...); err == nil {
			t.Fatal("invalid merge accepted")
		}
	}
}
