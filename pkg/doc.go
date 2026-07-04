// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pkg implements the GitHub build watcher service.
// It polls the GitHub Actions API for failed CI workflow runs on default
// branches, derives a per-repo green/red state, and publishes
// CreateTaskCommand to Kafka on green → red transitions.
package pkg
