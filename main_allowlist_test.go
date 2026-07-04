// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("application Run — allowlist Validate boundary", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("Run with malformed REPO_ALLOWLIST entries", func() {
		It("fails fast with an aggregate error naming every malformed entry", func() {
			app := application{
				PollInterval:  "5m",
				RepoAllowlist: "github.com/bborbe/maintainer,bad-entry,also/bad",
				MaxTitleLen:   200,
			}
			err := app.Run(ctx, nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("bad-entry"))
			Expect(err.Error()).To(ContainSubstring("also/bad"))
		})
	})

	Context("Run with wildcard REPO_ALLOWLIST entry", func() {
		It("does not reject wildcard entries via Validate", func() {
			app := application{
				PollInterval:  "5m",
				RepoAllowlist: "github.com/bborbe/*",
				MaxTitleLen:   200,
			}
			err := app.Run(ctx, nil)
			// Validate passes for wildcards; error comes from a later step (e.g. Kafka).
			// The important assertion: no repo-allowlist-related error.
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("REPO_ALLOWLIST contains malformed"))
			}
		})
	})
})
