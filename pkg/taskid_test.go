// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DeriveTaskID", func() {
	It("is deterministic — same inputs always produce the same UUID", func() {
		id1 := pkg.DeriveTaskID("bborbe", "maintainer", "abc123")
		id2 := pkg.DeriveTaskID("bborbe", "maintainer", "abc123")
		Expect(id1).To(Equal(id2))
	})

	It("produces different UUIDs for different episodeSHA values", func() {
		id1 := pkg.DeriveTaskID("bborbe", "maintainer", "abc123")
		id2 := pkg.DeriveTaskID("bborbe", "maintainer", "def456")
		Expect(id1).NotTo(Equal(id2))
	})

	It("produces different UUIDs for different repos", func() {
		id1 := pkg.DeriveTaskID("bborbe", "maintainer", "abc123")
		id2 := pkg.DeriveTaskID("bborbe", "other-repo", "abc123")
		Expect(id1).NotTo(Equal(id2))
	})

	It("produces a different UUID than the PR-watcher namespace for the same repo", func() {
		// PR watcher namespace: 7d4b3e5f-8a21-4c9d-b036-2e5f7a8c1d0e
		prWatcherNamespace := uuid.MustParse("7d4b3e5f-8a21-4c9d-b036-2e5f7a8c1d0e")
		key := "bborbe/maintainer#build-abc123"
		prDerived := uuid.NewSHA1(prWatcherNamespace, []byte(key))
		buildDerived := pkg.DeriveTaskID("bborbe", "maintainer", "abc123")
		Expect(buildDerived).NotTo(Equal(prDerived))
	})
})

var _ = Describe("DeriveTaskIDForce", func() {
	const (
		owner      = "bborbe"
		repo       = "maintainer"
		episodeSHA = "abc123def456abc123def456abc123def456abcd"
	)

	It("DeriveTaskIDForce differs from canonical for the same (owner, repo, episodeSHA)", func() {
		canonical := pkg.DeriveTaskID(owner, repo, episodeSHA)
		salted := pkg.DeriveTaskIDForce(owner, repo, episodeSHA, "x")
		Expect(salted).NotTo(Equal(canonical))
	})

	It("DeriveTaskIDForce stable for same nonce", func() {
		a := pkg.DeriveTaskIDForce(owner, repo, episodeSHA, "nonce-x")
		b := pkg.DeriveTaskIDForce(owner, repo, episodeSHA, "nonce-x")
		Expect(a).To(Equal(b))
	})

	It("DeriveTaskIDForce differs across nonces", func() {
		a := pkg.DeriveTaskIDForce(owner, repo, episodeSHA, "nonce-a")
		b := pkg.DeriveTaskIDForce(owner, repo, episodeSHA, "nonce-b")
		Expect(a).NotTo(Equal(b))
	})
})
