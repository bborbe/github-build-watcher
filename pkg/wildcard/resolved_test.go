// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wildcard_test

import (
	"context"
	"errors"
	"time"

	"github.com/bborbe/github-build-watcher/pkg/wildcard"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResolvedAllowlist", func() {
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

	Describe("RefreshInterval", func() {
		It("is one hour as per spec 039 Non-goal", func() {
			Expect(wildcard.RefreshInterval()).To(Equal(time.Hour))
		})
	})

	Describe("cold start semantics", func() {
		It("snapshot contains only literals before first successful refresh", func() {
			fake.reposByOwner["bborbe"] = []string{"a", "b"}
			input := []string{"github.com/bborbe/*", "github.com/bborbe/literal"}
			ral := wildcard.NewResolvedAllowlist(exp, input)
			// Snapshot after NewResolvedAllowlist: wildcard contributes nothing
			Expect(ral.Snapshot()).To(Equal([]string{"github.com/bborbe/literal"}))
		})

		It("first successful refresh populates snapshot with wildcard entries", func() {
			fake.reposByOwner["bborbe"] = []string{"a", "b"}
			input := []string{"github.com/bborbe/*", "github.com/bborbe/literal"}
			ral := wildcard.NewResolvedAllowlist(exp, input)
			_ = ral.Refresh(ctx)
			// Input order: wildcard first, literal second → output follows input order
			Expect(ral.Snapshot()).To(Equal([]string{
				"github.com/bborbe/a",
				"github.com/bborbe/b",
				"github.com/bborbe/literal",
			}))
		})
	})

	Describe("all-wildcards-fail with no prior snapshot", func() {
		It("snapshot remains unchanged and Refresh returns nil", func() {
			fake.errByOwner["bborbe"] = errors.New("boom")
			input := []string{"github.com/bborbe/*", "github.com/bborbe/literal"}
			ral := wildcard.NewResolvedAllowlist(exp, input)
			// Initial snapshot: only literal (wildcard contributed nothing)
			Expect(ral.Snapshot()).To(Equal([]string{"github.com/bborbe/literal"}))

			err := ral.Refresh(ctx)
			Expect(err).NotTo(HaveOccurred())
			// Snapshot still unchanged (all wildcards failed, no prior LKG)
			Expect(ral.Snapshot()).To(Equal([]string{"github.com/bborbe/literal"}))
		})
	})

	Describe("per-entry failure preserves last-known-good", func() {
		It("retains LKG for failed wildcard while updating successful ones", func() {
			// First refresh: both wildcards succeed
			fake.reposByOwner["bborbe"] = []string{"a", "b"}
			fake.reposByOwner["golang"] = []string{"x"}
			input := []string{"github.com/bborbe/*", "github.com/golang/*"}
			ral := wildcard.NewResolvedAllowlist(exp, input)
			_ = ral.Refresh(ctx)
			Expect(ral.Snapshot()).To(Equal([]string{
				"github.com/bborbe/a",
				"github.com/bborbe/b",
				"github.com/golang/x",
			}))

			// Second refresh: bborbe fails, golang succeeds with new value
			fake.errByOwner["bborbe"] = errors.New("boom")
			fake.reposByOwner["golang"] = []string{"y"}

			_ = ral.Refresh(ctx)
			Expect(ral.Snapshot()).To(Equal([]string{
				"github.com/bborbe/a",
				"github.com/bborbe/b", // preserved from first refresh
				"github.com/golang/y", // fresh from second refresh
			}))
		})
	})

	Describe("RunRefreshLoop", func() {
		It("exits cleanly when context is cancelled", func() {
			ctx, cancel := context.WithCancel(context.Background())
			fake.reposByOwner["bborbe"] = []string{"a"}

			input := []string{"github.com/bborbe/*"}
			ral := wildcard.NewResolvedAllowlist(exp, input)

			done := make(chan error, 1)
			go func() {
				done <- ral.RunRefreshLoop(ctx)
			}()

			// Give it a moment to start and issue the immediate refresh
			time.Sleep(50 * time.Millisecond)
			cancel()

			// Should exit within 200ms of cancellation
			Eventually(done, 1*time.Second).Should(Receive(BeNil()))
		})

		It("issues immediate refresh before first tick", func() {
			ctx, cancel := context.WithCancel(context.Background())
			fake.reposByOwner["bborbe"] = []string{"a"}

			input := []string{"github.com/bborbe/*"}
			ral := wildcard.NewResolvedAllowlist(exp, input)

			go func() {
				_ = ral.RunRefreshLoop(ctx)
			}()

			// Wait ~50ms for the immediate refresh to fire
			time.Sleep(50 * time.Millisecond)
			cancel()

			// bborbe should have been called exactly once (cold-start immediate refresh)
			Expect(fake.perOwnerCalls["bborbe"]).To(Equal(1))
		})
	})
})
