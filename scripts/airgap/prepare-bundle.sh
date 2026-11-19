#!/usr/bin/env bash
# Builds a bundle that runs the provider on an airgapped machine. Run from the repository root:
#
#   scripts/airgap/prepare-bundle.sh
#   ARCH=arm64 scripts/airgap/prepare-bundle.sh            # target says aarch64 for uname -m
#   CONFIG_DIR=path/to/config scripts/airgap/prepare-bundle.sh
#
# Produces dist/airgap/aw-airgap-bundle.tgz.
set -euo pipefail

ARCH=${ARCH:-amd64}                            # CPU of the airgapped machine: amd64 or arm64
VERSION=${VERSION:-$(cat VERSION)}
CONFIG_DIR=${CONFIG_DIR:-examples/airgap}      # the .tf and .json files to bundle
OUT_DIR=${OUT_DIR:-dist/airgap}

BUNDLE=$OUT_DIR/aw-airgap-bundle
PLUGIN_DIR=$BUNDLE/terraform.d/plugins/registry.terraform.io/insightfinder/aiwatchtower/$VERSION/linux_$ARCH

rm -rf "$BUNDLE" "$BUNDLE.tgz"
mkdir -p "$PLUGIN_DIR"

CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$PLUGIN_DIR/terraform-provider-aiwatchtower_v$VERSION" .

cp "$CONFIG_DIR"/*.tf "$CONFIG_DIR"/*.json scripts/airgap/run-aw-terraform.sh "$BUNDLE/"

tar czf "$BUNDLE.tgz" -C "$OUT_DIR" aw-airgap-bundle

echo "Send this file to the airgapped machine: $BUNDLE.tgz"
