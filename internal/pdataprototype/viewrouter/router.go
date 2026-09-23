// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package viewrouter is the prototype distribution's native attribute router.
package viewrouter

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/connector"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/xpdata/payload"
	"go.opentelemetry.io/collector/pdata/xpdata/plogpayload"
	"go.opentelemetry.io/collector/pdata/xpdata/pref"
	"go.opentelemetry.io/collector/pipeline"
)

type Config struct {
	Source string        `mapstructure:"source"`
	Key    string        `mapstructure:"key"`
	Value  string        `mapstructure:"value"`
	Match  []pipeline.ID `mapstructure:"match"`
	Other  []pipeline.ID `mapstructure:"other"`
}

func (c *Config) Validate() error {
	if c.Source != "resource" && c.Source != "log" && c.Source != "scope" {
		return errors.New("source must be resource, scope, or log")
	}
	if c.Key == "" || len(c.Match) == 0 || len(c.Other) == 0 {
		return errors.New("key and both output pipeline lists are required")
	}
	return nil
}

func NewFactory() connector.Factory {
	return connector.NewFactory(component.MustNewType("view_route"),
		func() component.Config { return &Config{Source: "resource"} },
		connector.WithLogsToLogs(create, component.StabilityLevelDevelopment))
}

func create(_ context.Context, _ connector.Settings, cfg component.Config, next consumer.Logs) (connector.Logs, error) {
	if !plogpayload.FeatureGate.IsEnabled() {
		return nil, errors.New("view_route requires service.PluggableLogs")
	}
	routes, ok := next.(connector.LogsRouterAndConsumer)
	if !ok {
		return nil, errors.New("view_route requires a logs router")
	}
	c := cfg.(*Config)
	match, err := routes.Consumer(c.Match...)
	if err != nil {
		return nil, err
	}
	other, err := routes.Consumer(c.Other...)
	if err != nil {
		return nil, err
	}
	reg, err := plogpayload.NewRegistry()
	if err != nil {
		return nil, err
	}
	return &router{cfg: c, match: match, other: other, registry: reg}, nil
}

type router struct {
	component.StartFunc
	component.ShutdownFunc
	cfg          *Config
	match, other consumer.Logs
	registry     *payload.Registry
}

func (*router) Capabilities() consumer.Capabilities { return consumer.Capabilities{MutatesData: false} }

func (r *router) ConsumeLogs(ctx context.Context, ld plog.Logs) error {
	pref.RefLogs(ld)
	p, err := plogpayload.NewFromLogs(r.registry, ld)
	if err != nil {
		pref.UnrefLogs(ld)
		return err
	}
	defer p.Release()
	return r.ConsumeLogsPayload(ctx, p)
}

func (r *router) ConsumeLogsPayload(ctx context.Context, p *payload.Payload) error {
	yes, no, err := p.Partition(func(resource payload.ResourceLogs, scope payload.ScopeLogs, log payload.LogRecord) (bool, error) {
		attrs := resource.Attributes
		switch r.cfg.Source {
		case "scope":
			attrs = scope.Attributes
		case "log":
			attrs = log.Attributes
		}
		v, exists, err := attrs.Lookup(r.cfg.Key)
		if err != nil || !exists {
			return false, err
		}
		s, ok := v.(string)
		if !ok {
			return false, fmt.Errorf("routing attribute %q must be a string, got %T", r.cfg.Key, v)
		}
		return s == r.cfg.Value, nil
	})
	if err != nil {
		return consumererror.NewPermanent(err)
	}
	var result error
	if yes != nil {
		result = consumer.ConsumeLogsPayload(ctx, r.match, yes)
		yes.Release()
	}
	if no != nil {
		result = errors.Join(result, consumer.ConsumeLogsPayload(ctx, r.other, no))
		no.Release()
	}
	return result
}
