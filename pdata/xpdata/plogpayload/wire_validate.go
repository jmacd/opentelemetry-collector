// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package plogpayload

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// Validate known wire fields without allocating pdata or generic value trees.
// Unknown fields remain opaque and survive forwarding/splitting.
func validateMessage(buf []byte, message string, depth int) error {
	if depth > 100 {
		return errors.New("protobuf nesting exceeds 100")
	}
	return walkWire(buf, func(f wireField) error {
		kind := protowire.Type(-1)
		child, isText, idSize := "", false, 0
		switch message {
		case "resource":
			switch f.number {
			case 1:
				child = "kv"
			case 2:
				kind = protowire.VarintType
			case 3:
				child = "entity"
			}
		case "entity":
			if f.number >= 1 && f.number <= 4 {
				isText = true
			}
		case "scope":
			switch f.number {
			case 1, 2:
				isText = true
			case 3:
				child = "kv"
			case 4:
				kind = protowire.VarintType
			}
		case "log":
			switch f.number {
			case 1, 11:
				kind = protowire.Fixed64Type
			case 2, 7:
				kind = protowire.VarintType
			case 3, 12:
				isText = true
			case 5:
				child = "value"
			case 6:
				child = "kv"
			case 8:
				kind = protowire.Fixed32Type
			case 9:
				idSize = 16
			case 10:
				idSize = 8
			}
		case "kv":
			switch f.number {
			case 1:
				isText = true
			case 2:
				child = "value"
			}
		case "value":
			switch f.number {
			case 1:
				isText = true
			case 2, 3:
				kind = protowire.VarintType
			case 4:
				kind = protowire.Fixed64Type
			case 5:
				child = "array"
			case 6:
				child = "map"
			case 7:
				kind = protowire.BytesType
			}
		case "array":
			if f.number == 1 {
				child = "value"
			}
		case "map":
			if f.number == 1 {
				child = "kv"
			}
		}
		if child != "" || isText || idSize > 0 {
			kind = protowire.BytesType
		}
		if kind == -1 {
			return nil
		}
		if f.kind != kind {
			return fmt.Errorf("invalid %s field %d wire type", message, f.number)
		}
		if isText && !utf8.Valid(f.bytes) {
			return errors.New("invalid protobuf UTF-8")
		}
		if idSize > 0 && len(f.bytes) != 0 && len(f.bytes) != idSize {
			return fmt.Errorf("invalid %s identifier length", message)
		}
		if child != "" {
			return validateMessage(f.bytes, child, depth+1)
		}
		return nil
	})
}
