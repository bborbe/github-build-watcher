// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package filter_test

import (
	"github.com/bborbe/github-build-watcher/pkg/filter"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RepoAllowlistFilter", func() {
	Context("with empty allowlist", func() {
		It("never skips (allow-all)", func() {
			f := filter.NewRepoAllowlistFilter(nil)
			Expect(f.Skip("github.com/owner/repo")).To(BeFalse())
			Expect(f.Skip("github.com/any/repo")).To(BeFalse())
		})
	})

	Context("with non-empty allowlist", func() {
		It("skips repos not in the list", func() {
			f := filter.NewRepoAllowlistFilter(
				[]string{"github.com/owner/allowed", "github.com/owner/also-allowed"},
			)
			Expect(f.Skip("github.com/owner/not-allowed")).To(BeTrue())
			Expect(f.Skip("github.com/other/repo")).To(BeTrue())
		})

		It("passes repos in the list", func() {
			f := filter.NewRepoAllowlistFilter(
				[]string{"github.com/owner/allowed", "github.com/owner/also-allowed"},
			)
			Expect(f.Skip("github.com/owner/allowed")).To(BeFalse())
			Expect(f.Skip("github.com/owner/also-allowed")).To(BeFalse())
		})
	})
})

var _ = Describe("RepoFilters OR-composite", func() {
	It("never skips when empty", func() {
		var fs filter.RepoFilters
		Expect(fs.Skip("github.com/owner/repo")).To(BeFalse())
	})

	It("skips if any filter votes skip", func() {
		fs := filter.RepoFilters{
			filter.NewRepoAllowlistFilter([]string{"github.com/owner/a"}),
			filter.NewRepoAllowlistFilter([]string{"github.com/owner/b"}),
		}
		// "github.com/owner/c" is not in either allowlist → at least one skips
		Expect(fs.Skip("github.com/owner/c")).To(BeTrue())
	})

	It("does not skip when all filters pass", func() {
		fs := filter.RepoFilters{
			filter.NewRepoAllowlistFilter(
				[]string{"github.com/owner/a", "github.com/owner/b"},
			),
			filter.NewRepoAllowlistFilter(nil), // allow-all
		}
		Expect(fs.Skip("github.com/owner/a")).To(BeFalse())
	})
})
