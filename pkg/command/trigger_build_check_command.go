// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command

import (
	"context"

	"github.com/bborbe/cqrs/base"
)

// TriggerBuildCheckCommandOperation is the Kafka command operation for
// triggering a github-build poll cycle. Wire string: "trigger-build-check".
const TriggerBuildCheckCommandOperation base.CommandOperation = "trigger-build-check"

// TriggerBuildCheckCommand is the payload for TriggerBuildCheckCommandOperation.
// It is published to the github-build watcher's request topic by the /trigger
// HTTP handler and consumed by the in-pod command consumer.
//
// Scope is reserved for a future per-repo filter UX; the executor still
// ignores it. Force is wired (spec 069): when true, the consuming watcher's
// red×red episode-lock arm publishes a salted CreateTaskCommand via
// pkg.DeriveTaskIDForce instead of skipping — operators can force a
// re-publish for a still-red build even when the episode is already locked.
// All other state-machine arms (green→red, red→green) ignore Force.
type TriggerBuildCheckCommand struct {
	Scope string `json:"scope,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// Validate enforces the command's schema rules. The empty payload {} is
// still accepted: Force defaults to false (engages the episode-lock skip,
// the canonical poll-loop behaviour), and Scope remains reserved-unread.
// A future spec will add per-repo or per-stage validation here.
func (cmd TriggerBuildCheckCommand) Validate(_ context.Context) error {
	return nil
}
