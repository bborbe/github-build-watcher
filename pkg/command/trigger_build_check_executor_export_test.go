// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command

import (
	"context"

	"github.com/bborbe/cqrs/base"
	cdb "github.com/bborbe/cqrs/cdb"
	"github.com/bborbe/github-build-watcher/pkg"
	libkv "github.com/bborbe/kv"
)

// RunTriggerBuildCheck re-exports the private runTriggerBuildCheck for
// the external test package. The _test.go suffix keeps this file
// out of production builds.
var RunTriggerBuildCheck = runTriggerBuildCheck

// Compile-time guard: keep the public surface tightly aligned with
// the internal helper. If runTriggerBuildCheck's signature ever drifts,
// this file fails to build and the test breakage is local.
var _ = func(
	ctx context.Context,
	tx libkv.Tx,
	obj cdb.CommandObject,
	watcher pkg.Watcher,
) (*base.EventID, base.Event, error) {
	return runTriggerBuildCheck(ctx, tx, obj, watcher)
}
