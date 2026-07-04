// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package wildcard expands owner-level wildcard allowlist entries
// (e.g. "github.com/bborbe/*") into concrete "host/owner/repo" entries
// by listing the owner's repositories via the GitHub API. The resolved
// entry slice is held in a thread-safe snapshot, refreshed once an hour
// by a background goroutine.
//
// Allowlists that contain zero wildcard entries do NOT trigger any
// API calls and do NOT start a refresh goroutine — they are returned
// through the package unchanged so pure-literal behavior is byte-identical
// to the pre-wildcard code path.
package wildcard
