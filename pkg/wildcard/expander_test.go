// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wildcard_test

import (
	"context"
	"errors"

	"github.com/bborbe/github-build-watcher/pkg/wildcard"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeOwnerLister struct {
	reposByOwner  map[string][]string
	errByOwner    map[string]error
	callCount     int
	perOwnerCalls map[string]int
}

func newFakeOwnerLister() *fakeOwnerLister {
	return &fakeOwnerLister{
		reposByOwner:  map[string][]string{},
		errByOwner:    map[string]error{},
		perOwnerCalls: map[string]int{},
	}
}

func (f *fakeOwnerLister) ListOwnerRepos(_ context.Context, owner string) ([]string, error) {
	f.callCount++
	f.perOwnerCalls[owner]++
	if err, ok := f.errByOwner[owner]; ok && err != nil {
		return nil, err
	}
	return append([]string(nil), f.reposByOwner[owner]...), nil
}

var _ = Describe("Expander", func() {
	var (
		fake *fakeOwnerLister
		exp  *wildcard.Expander
		ctx  context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake = newFakeOwnerLister()
		exp = wildcard.NewExpander(fake)
	})

	Describe("HasWildcard", func() {
		It("returns false for pure-literal allowlist", func() {
			input := []string{"github.com/bborbe/a", "github.com/bborbe/b"}
			Expect(wildcard.HasWildcard(input)).To(BeFalse())
		})

		It("returns true when any entry is a wildcard", func() {
			input := []string{"github.com/bborbe/*"}
			Expect(wildcard.HasWildcard(input)).To(BeTrue())
		})

		It("returns true for mixed allowlist", func() {
			input := []string{"github.com/bborbe/literal", "github.com/bborbe/*"}
			Expect(wildcard.HasWildcard(input)).To(BeTrue())
		})
	})

	Describe("Expand", func() {
		Context("pure-literal allowlist", func() {
			It("returns literals unchanged", func() {
				input := []string{"github.com/bborbe/a", "github.com/bborbe/b"}
				result, err := exp.Expand(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal(input))
			})

			It("makes zero API calls", func() {
				input := []string{"github.com/bborbe/a", "github.com/bborbe/b"}
				_, _ = exp.Expand(ctx, input)
				Expect(fake.callCount).To(BeZero())
			})
		})

		Context("mixed allowlist with literal and wildcard", func() {
			It("returns literals and expanded wildcards without duplication", func() {
				fake.reposByOwner["bborbe"] = []string{"repo-a", "other"}
				input := []string{"github.com/bborbe/literal", "github.com/bborbe/*"}
				result, err := exp.Expand(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal([]string{
					"github.com/bborbe/literal",
					"github.com/bborbe/repo-a",
					"github.com/bborbe/other",
				}))
			})
		})

		Context("wildcard expansion", func() {
			It("expands wildcard to concrete entries", func() {
				fake.reposByOwner["bborbe"] = []string{"repo-a", "repo-d"}
				input := []string{"github.com/bborbe/*"}
				result, err := exp.Expand(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(Equal([]string{
					"github.com/bborbe/repo-a",
					"github.com/bborbe/repo-d",
				}))
			})
		})

		Context("wildcard with API error", func() {
			It("returns wrapped error and skips that wildcard's contribution", func() {
				fake.errByOwner["bborbe"] = errors.New("boom")
				input := []string{"github.com/bborbe/literal", "github.com/bborbe/*"}
				result, err := exp.Expand(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("resolve wildcard github.com/bborbe/*"))
				Expect(result).To(Equal([]string{"github.com/bborbe/literal"}))
			})
		})

		Context("multiple wildcards", func() {
			It("calls each owner once per wildcard entry", func() {
				fake.reposByOwner["bborbe"] = []string{"repo-1"}
				fake.reposByOwner["golang"] = []string{"go-repo"}
				input := []string{"github.com/bborbe/*", "github.com/golang/*"}
				_, err := exp.Expand(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(fake.perOwnerCalls["bborbe"]).To(Equal(1))
				Expect(fake.perOwnerCalls["golang"]).To(Equal(1))
			})
		})
	})
})
