// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package pdataprototype

import (
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/provider/envprovider"
	"go.opentelemetry.io/collector/confmap/provider/fileprovider"
	"go.opentelemetry.io/collector/confmap/provider/yamlprovider"
	"go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/exporter/debugexporter"
	"go.opentelemetry.io/collector/exporter/otlpexporter"
	"go.opentelemetry.io/collector/exporter/otlphttpexporter"
	"go.opentelemetry.io/collector/internal/pdataprototype/viewrouter"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	"go.opentelemetry.io/collector/service/telemetry/otelconftelemetry"
)

func Factories() (otelcol.Factories, error) {
	f := otelcol.Factories{Telemetry: otelconftelemetry.NewFactory()}
	var err error
	f.Receivers, err = otelcol.MakeFactoryMap[receiver.Factory](otlpreceiver.NewFactory())
	if err != nil {
		return f, err
	}
	f.Exporters, err = otelcol.MakeFactoryMap[exporter.Factory](debugexporter.NewFactory(), otlphttpexporter.NewFactory(), otlpexporter.NewFactory())
	if err != nil {
		return f, err
	}
	f.Connectors, err = otelcol.MakeFactoryMap[connector.Factory](viewrouter.NewFactory())
	return f, err
}

func Settings() otelcol.CollectorSettings {
	return otelcol.CollectorSettings{
		Factories: Factories,
		ConfigProviderSettings: otelcol.ConfigProviderSettings{ResolverSettings: confmap.ResolverSettings{
			ProviderFactories: []confmap.ProviderFactory{envprovider.NewFactory(), fileprovider.NewFactory(), yamlprovider.NewFactory()},
			DefaultScheme:     "file",
		}},
	}
}
