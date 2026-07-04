// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("computeBuildTitle", func() {
	DescribeTable(
		"produces correct title",
		func(provider, owner, repo, sha string, maxTitle int, taskSuffix, want string) {
			Expect(
				computeBuildTitle(provider, owner, repo, sha, maxTitle, taskSuffix),
			).To(Equal(want))
		},
		Entry(
			"taskSuffix empty: legacy filename without trailing separator",
			"github",
			"bborbe",
			"maintainer",
			"5886450abcdef",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - bborbe-maintainer - 5886450",
		),
		Entry(
			"taskSuffix=dev appended with hyphen separator",
			"github",
			"bborbe",
			"maintainer",
			"5886450abcdef",
			DefaultMaxTitleLen,
			"dev",
			"Build Failure github - bborbe-maintainer - 5886450 - dev",
		),
		Entry(
			"taskSuffix=prod appended with hyphen separator",
			"github",
			"bborbe",
			"maintainer",
			"5886450abcdef",
			DefaultMaxTitleLen,
			"prod",
			"Build Failure github - bborbe-maintainer - 5886450 - prod",
		),
		Entry(
			"truncation preserves suffix at end when title exceeds maxTitle",
			"github",
			"b",
			"r",
			"1234567890abcdef",
			40,
			"dev",
			"Build Failure github - b-r - 12345 - dev",
		),
	)
})
