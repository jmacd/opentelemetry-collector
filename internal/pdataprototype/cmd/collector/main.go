// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"go.opentelemetry.io/collector/internal/pdataprototype"
	"go.opentelemetry.io/collector/otelcol"
)

func main() {
	if err := otelcol.NewCommand(pdataprototype.Settings()).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
