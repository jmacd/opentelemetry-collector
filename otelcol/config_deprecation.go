// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otelcol // import "go.opentelemetry.io/collector/otelcol"

import (
	"context"
	"slices"
	"strings"
	"sync"

	"go.uber.org/zap"

	"go.opentelemetry.io/collector/component/componentdeprecation"
	"go.opentelemetry.io/collector/confmap"
)

// The builder knows the exact Go import path, even when multiple factories
// share a module. Older distributions supply module information for root packages.
func configFactoryPackage(factory any, module string) string {
	if f, ok := factory.(interface{ ConfigFactoryPackage() string }); ok {
		return f.ConfigFactoryPackage()
	}
	path, _, _ := strings.Cut(module, " ")
	return path
}

func bindConfigDeprecations(set *CollectorSettings) {
	rs := &set.ConfigProviderSettings.ResolverSettings
	rs.ProviderFactories = slices.Clone(rs.ProviderFactories)
	modules := set.ProviderModules
	for i, f := range rs.ProviderFactories {
		rs.ProviderFactories[i] = confmap.NewProviderFactory(func(ps confmap.ProviderSettings) confmap.Provider {
			p := f.Create(ps)
			scheme := p.Scheme()
			pkg := configFactoryPackage(f, modules[scheme])
			if write := componentdeprecation.Lookup("provider", pkg, "provider"); write != nil {
				return noticeProvider{p, sync.OnceFunc(func() { write(ps.Logger.With(zap.String("provider", scheme))) })}
			}
			return p
		})
	}
	rs.ConverterFactories = slices.Clone(rs.ConverterFactories)
	for i, f := range rs.ConverterFactories {
		var module string
		if i < len(set.ConverterModules) {
			module = set.ConverterModules[i]
		}
		pkg := configFactoryPackage(f, module)
		if write := componentdeprecation.Lookup("converter", pkg, "converter"); write != nil {
			rs.ConverterFactories[i] = confmap.NewConverterFactory(func(cs confmap.ConverterSettings) confmap.Converter {
				return noticeConverter{f.Create(cs), sync.OnceFunc(func() { write(cs.Logger.With(zap.String("converter", pkg))) })}
			})
		}
	}
}

type noticeProvider struct {
	confmap.Provider
	notify func()
}

func (p noticeProvider) Retrieve(ctx context.Context, uri string, watcher confmap.WatcherFunc) (*confmap.Retrieved, error) {
	p.notify()
	return p.Provider.Retrieve(ctx, uri, watcher)
}

type noticeConverter struct {
	confmap.Converter
	notify func()
}

func (c noticeConverter) Convert(ctx context.Context, cfg *confmap.Conf) error {
	c.notify()
	return c.Converter.Convert(ctx, cfg)
}
