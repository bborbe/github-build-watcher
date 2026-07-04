// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("countWildcards", func() {
	DescribeTable(
		"returns correct wildcard count",
		func(entries []string, want int) {
			Expect(countWildcards(entries)).To(Equal(want))
		},
		Entry("empty slice", []string{}, 0),
		Entry("no wildcards", []string{"github.com/bborbe/repo", "github.com/owner/project"}, 0),
		Entry("one wildcard", []string{"github.com/bborbe/*"}, 1),
		Entry(
			"multiple wildcards",
			[]string{"github.com/bborbe/*", "github.com/org/*", "bitbucket.org/team/*"},
			3,
		),
		Entry("malformed entry with 4 segments", []string{"github.com/bborbe/repo/extra"}, 0),
		Entry("wildcard not at position 3", []string{"github.com/bborbe/repo"}, 0),
		Entry(
			"mixed valid and invalid entries",
			[]string{
				"github.com/bborbe/*",
				"github.com/owner/repo",
				"bitbucket.org/team/*",
				"bad/entry/with/too/many",
			},
			2,
		),
	)
})
