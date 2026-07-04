// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package filter implements the RepoFilter chain for the build watcher.
// Filters decide whether a repository should be skipped in a poll cycle.

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6@v6.12.2 -generate

package filter

//counterfeiter:generate -o ../../mocks/repo_filter.go --fake-name RepoFilter . RepoFilter

// RepoFilter decides whether to skip a repo in a poll cycle.
// Skip returns true if the repo should be excluded.
type RepoFilter interface {
	Skip(repoKey string) bool // repoKey = "host/owner/repo"
}

// RepoFilters is an OR-composite: skip if ANY filter votes to skip.
// An empty slice never skips (allow-all).
type RepoFilters []RepoFilter

// Skip returns true if any contained filter votes skip.
func (filters RepoFilters) Skip(repoKey string) bool {
	for _, f := range filters {
		if f.Skip(repoKey) {
			return true
		}
	}
	return false
}
