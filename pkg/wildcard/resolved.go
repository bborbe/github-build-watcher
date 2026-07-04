// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wildcard

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/glog"
)

// refreshInterval is the cadence at which the background refresh goroutine
// re-resolves every wildcard entry. Hardcoded by spec 039 Non-goal:
// "Do NOT add a refresh-interval knob — invariant at one hour."
const refreshInterval = time.Hour

// ResolvedAllowlist holds the current resolved entry slice (concrete
// host/owner/repo strings; wildcards already expanded).
//
// Reads via Snapshot are wait-free (atomic pointer load).
// Writes via Refresh hold an internal mutex to avoid two concurrent
// refreshes overlapping.
type ResolvedAllowlist struct {
	expander *Expander
	input    []string

	snapshot atomic.Pointer[[]string]

	refreshMu sync.Mutex // held for the lifetime of one Refresh call
}

// NewResolvedAllowlist returns a ResolvedAllowlist seeded with the given
// input allowlist (wildcards NOT yet expanded). The snapshot starts as a
// copy of input MINUS any wildcard entries — so callers that read the
// snapshot before the first successful Refresh see only the literals,
// matching spec AC: "Cold start: initial resolution fails ... wildcards
// contribute zero entries until first successful refresh; literal entries
// poll normally."
func NewResolvedAllowlist(expander *Expander, input []string) *ResolvedAllowlist {
	seed := make([]string, 0, len(input))
	for _, entry := range input {
		if !isWildcardEntry(entry) {
			seed = append(seed, entry)
		}
	}
	r := &ResolvedAllowlist{
		expander: expander,
		input:    append([]string(nil), input...),
	}
	r.snapshot.Store(&seed)
	return r
}

// Snapshot returns the current resolved entry slice. The returned slice
// is safe to iterate while a concurrent Refresh is in progress; the
// refresh swaps the pointer atomically.
//
// Callers MUST NOT mutate the returned slice.
func (r *ResolvedAllowlist) Snapshot() []string {
	p := r.snapshot.Load()
	if p == nil {
		return nil
	}
	return *p
}

// Refresh re-resolves every wildcard entry against the GitHub API and
// updates the snapshot. On per-wildcard failure the existing
// contribution for that wildcard is retained (last-known-good fallback);
// if ALL wildcards fail, the snapshot is left untouched. Emits a
// glog.V(2) "wildcard_expanded ... source=last-known-good" line for each
// wildcard whose refresh failed but had a previously-resolved value.
//
// Refresh is safe to call from multiple goroutines but serializes via
// an internal mutex — concurrent callers wait their turn.
func (r *ResolvedAllowlist) Refresh(ctx context.Context) error {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	prev := r.Snapshot()
	prevByWildcard := groupByWildcard(r.input, prev)

	result := make([]string, 0, len(prev))
	seen := make(map[string]struct{}, len(prev))
	add := func(entry string) {
		if _, ok := seen[entry]; ok {
			return
		}
		seen[entry] = struct{}{}
		result = append(result, entry)
	}

	anyFreshSuccess := false
	allWildcardsFailed := true
	hadAnyWildcard := false

	for _, entry := range r.input {
		if !isWildcardEntry(entry) {
			add(entry)
			continue
		}
		hadAnyWildcard = true
		host, owner := splitWildcardEntry(entry)
		names, err := r.expander.client.ListOwnerRepos(ctx, owner)
		if err != nil {
			glog.Warningf(
				"wildcard_refresh_failed entry=%s reason=%v",
				entry, err,
			)
			// Fallback: reuse last-known-good entries for this wildcard.
			for _, lkg := range prevByWildcard[entry] {
				add(lkg)
			}
			if len(prevByWildcard[entry]) > 0 {
				glog.V(2).Infof(
					"wildcard_expanded entry=%s resolved_count=%d source=last-known-good",
					entry, len(prevByWildcard[entry]),
				)
			}
			continue
		}
		anyFreshSuccess = true
		allWildcardsFailed = false
		glog.V(2).Infof(
			"wildcard_expanded entry=%s resolved_count=%d source=fresh",
			entry, len(names),
		)
		for _, name := range names {
			add(host + "/" + owner + "/" + name)
		}
	}

	// Spec failure mode: "Resolved set used by the poll loop is updated
	// atomically at the end of each successful refresh." If literally
	// every wildcard failed AND no fresh success occurred, do NOT swap
	// the snapshot — leave the prior pointer in place.
	if hadAnyWildcard && allWildcardsFailed && !anyFreshSuccess {
		return nil
	}

	r.snapshot.Store(&result)
	return nil
}

// RunRefreshLoop blocks until ctx is cancelled, calling Refresh once
// per refreshInterval. The first Refresh is invoked IMMEDIATELY (not
// after the first tick) so the snapshot is populated as fast as
// possible after startup. Panics in Refresh are recovered and logged;
// the loop re-arms for the next tick (spec failure mode: "Refresh
// goroutine panics → Recover, log error at V(0), re-arm the next
// refresh tick").
//
// Returns nil when ctx is cancelled. Never returns an error otherwise.
func (r *ResolvedAllowlist) RunRefreshLoop(ctx context.Context) error {
	r.safeRefresh(ctx)

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.safeRefresh(ctx)
		}
	}
}

// safeRefresh calls Refresh with a panic-recover guard so a panic in
// the GitHub client implementation cannot kill the goroutine.
func (r *ResolvedAllowlist) safeRefresh(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			glog.Errorf("wildcard refresh panic recovered: %v", rec)
		}
	}()
	if err := r.Refresh(ctx); err != nil {
		glog.Warningf("wildcard refresh error: %v", err)
	}
}

// groupByWildcard partitions a resolved entry slice into per-wildcard
// buckets based on the input allowlist. Used by Refresh to retain the
// last-known-good entries for a wildcard whose fresh API call failed.
//
// An entry "host/owner/repo" is attributed to wildcard "host/owner/*"
// when their host and owner segments match. Literal entries that also
// happen to match a wildcard (e.g. the input "github.com/bborbe/repo-a"
// alongside "github.com/bborbe/*") are NOT placed in the wildcard
// bucket — they appear independently in the input loop and would be
// double-counted otherwise.
func groupByWildcard(input, resolved []string) map[string][]string {
	wildcards := make([]string, 0, len(input))
	literals := make(map[string]struct{}, len(input))
	for _, entry := range input {
		if isWildcardEntry(entry) {
			wildcards = append(wildcards, entry)
		} else {
			literals[entry] = struct{}{}
		}
	}
	out := make(map[string][]string, len(wildcards))
	for _, entry := range resolved {
		if _, isLit := literals[entry]; isLit {
			continue
		}
		for _, w := range wildcards {
			wHost, wOwner := splitWildcardEntry(w)
			eHost, eOwner, ok := splitResolvedEntry(entry)
			if !ok {
				continue
			}
			if eHost == wHost && eOwner == wOwner {
				out[w] = append(out[w], entry)
				break
			}
		}
	}
	return out
}

// splitResolvedEntry splits "host/owner/repo" into its three segments.
// Returns ok=false when the entry does not have exactly three segments.
func splitResolvedEntry(entry string) (host, owner string, ok bool) {
	i := 0
	for j := 0; j < len(entry); j++ {
		if entry[j] == '/' {
			switch i {
			case 0:
				host = entry[:j]
			case 1:
				owner = entry[len(host)+1 : j]
				return host, owner, true
			}
			i++
		}
	}
	return "", "", false
}

// RefreshInterval returns the (constant) wildcard refresh cadence.
// Exposed for assertion in tests that verify spec 039's
// "invariant at one hour" Non-goal is not regressed.
func RefreshInterval() time.Duration { return refreshInterval }
