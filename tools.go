// Copyright (c) InsightFinder Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build tools

// Package tools pins the code-generation binaries this module invokes through `go generate`.
//
// The build tag keeps these imports out of every real build; their only purpose is to put
// tfplugindocs in go.mod so `go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs`
// resolves to a pinned version instead of failing with "no required module provides package".
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)
