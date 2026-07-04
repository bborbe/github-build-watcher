// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wildcard

import (
	"context"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// ownerLister is the subset of pkg.GitHubClient the expander needs.
// Defining it locally lets the expander tests use a small fake that
// does not have to implement the full pkg.GitHubClient surface.
type ownerLister interface {
	ListOwnerRepos(ctx context.Context, owner string) ([]string, error)
}

// Expander resolves wildcard allowlist entries against the GitHub API.
type Expander struct {
	client ownerLister
}

// NewExpander returns an Expander that uses the given client.
func NewExpander(client ownerLister) *Expander {
	return &Expander{client: client}
}

// HasWildcard reports whether any entry in the allowlist is a wildcard
// entry ("host/owner/*"). Callers use this to short-circuit refresh
// setup when the allowlist is pure-literal.
func HasWildcard(entries []string) bool {
	for _, entry := range entries {
		if isWildcardEntry(entry) {
			return true
		}
	}
	return false
}

// Expand resolves every wildcard entry in input against the GitHub API
// and returns a deduplicated slice of concrete "host/owner/repo" entries
// (literals pass through unchanged). Resolution order: input order, with
// each wildcard expanded in-place; literals that also appear in a
// wildcard's expansion are NOT duplicated.
//
// Per-entry failures: ANY API error for a given wildcard entry causes
// that wildcard's contribution to be empty in the returned slice and the
// error is returned wrapped with the entry name. Callers that hold a
// previously-resolved snapshot use ResolvedAllowlist.Refresh (see
// resolved.go) to merge fresh results with last-known-good fallback.
//
// Emits one glog.V(2) line per wildcard resolution naming the entry,
// the resolved count, and source="fresh".
func (e *Expander) Expand(ctx context.Context, input []string) ([]string, error) {
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	add := func(entry string) {
		if _, ok := seen[entry]; ok {
			return
		}
		seen[entry] = struct{}{}
		result = append(result, entry)
	}
	var firstErr error
	for _, entry := range input {
		if !isWildcardEntry(entry) {
			add(entry)
			continue
		}
		host, owner := splitWildcardEntry(entry)
		names, err := e.client.ListOwnerRepos(ctx, owner)
		if err != nil {
			if firstErr == nil {
				firstErr = errors.Wrapf(ctx, err, "resolve wildcard %s", entry)
			}
			continue
		}
		glog.V(2).Infof(
			"wildcard_expanded entry=%s resolved_count=%d source=fresh",
			entry, len(names),
		)
		for _, name := range names {
			add(host + "/" + owner + "/" + name)
		}
	}
	return result, firstErr
}

// isWildcardEntry reports whether entry has the shape "host/owner/*".
func isWildcardEntry(entry string) bool {
	segments := strings.Split(strings.TrimSpace(entry), "/")
	return len(segments) == 3 && segments[2] == "*"
}

// splitWildcardEntry returns (host, owner) for a wildcard entry.
// Callers must have already verified isWildcardEntry(entry) == true.
func splitWildcardEntry(entry string) (host, owner string) {
	segments := strings.Split(strings.TrimSpace(entry), "/")
	return segments[0], segments[1]
}
