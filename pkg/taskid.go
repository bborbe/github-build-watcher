// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"github.com/google/uuid"
)

// buildWatcherNamespace is the fixed v5 UUID namespace for all build-watcher task identifiers.
// Distinct from prWatcherNamespace to prevent cross-service ID collisions.
var buildWatcherNamespace = uuid.MustParse("8e3f5a2c-7b14-4d9e-a017-3c6e8b9f2a1d")

// DeriveTaskID returns a deterministic task identifier for a build-failure episode.
// Input: "<owner>/<repo>#build-<episodeSHA>", e.g. "bborbe/maintainer#build-abc123".
func DeriveTaskID(owner, repo, episodeSHA string) uuid.UUID {
	key := owner + "/" + repo + "#build-" + episodeSHA
	return uuid.NewSHA1(buildWatcherNamespace, []byte(key))
}

// DeriveTaskIDForce returns a salted task identifier for an operator-forced
// re-publish of a build-failure episode (spec 069). The salt is a caller-supplied
// nonce — typically a microsecond timestamp from libtime.CurrentDateTimeGetter — so
// successive forced re-publishes for the same (owner, repo, episodeSHA) produce
// distinct identifiers and the agent controller's deterministic-ID dedup-skip does
// NOT fire. Pure function; nonce uniqueness is the caller's responsibility.
//
// Key format: "<owner>/<repo>#build-<episodeSHA>!<nonce>". The "!" separator is
// invalid in GitHub owners/repos and in hex SHAs, so the salted keyspace cannot
// collide with the canonical DeriveTaskID keyspace for any (owner, repo, sha) tuple.
func DeriveTaskIDForce(owner, repo, episodeSHA, nonce string) uuid.UUID {
	key := owner + "/" + repo + "#build-" + episodeSHA + "!" + nonce
	return uuid.NewSHA1(buildWatcherNamespace, []byte(key))
}
