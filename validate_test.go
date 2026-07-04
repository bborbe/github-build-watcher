// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("validateMaxTitleLen",
	func(maxTitleLen int, expectError bool, errContains string) {
		ctx := context.Background()
		err := validateMaxTitleLen(ctx, maxTitleLen)
		if expectError {
			Expect(err).To(HaveOccurred())
			if errContains != "" {
				Expect(err.Error()).To(ContainSubstring(errContains))
			}
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
	Entry("valid default", 200, false, ""),
	Entry("custom valid value", 50, false, ""),
	Entry("MaxTitleLen=0 is rejected", 0, true, "MAX_TITLE_LEN must be > 0"),
	Entry("MaxTitleLen=-1 is rejected", -1, true, "MAX_TITLE_LEN must be > 0"),
	Entry("MaxTitleLen=1 is accepted", 1, false, ""),
)
