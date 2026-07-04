// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bborbe/github-build-watcher/pkg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cursor", func() {
	var ctx context.Context
	var tmpDir string

	BeforeEach(func() {
		ctx = context.Background()
		tmpDir = GinkgoT().TempDir()
	})

	cursorPath := func() string {
		return filepath.Join(tmpDir, "cursor.json")
	}

	Describe("LoadCursor", func() {
		Context("when cursor file does not exist", func() {
			It("returns a fresh empty cursor without error", func() {
				c, err := pkg.LoadCursor(ctx, cursorPath())
				Expect(err).NotTo(HaveOccurred())
				Expect(c).NotTo(BeNil())
				Expect(c.Repos).NotTo(BeNil())
				Expect(c.Repos).To(BeEmpty())
			})
		})

		Context("when cursor file contains corrupt JSON", func() {
			It("returns an error", func() {
				Expect(os.WriteFile(cursorPath(), []byte("not-valid-json{{{"), 0600)).To(Succeed())
				c, err := pkg.LoadCursor(ctx, cursorPath())
				Expect(err).To(HaveOccurred())
				Expect(c).To(BeNil())
			})
		})

		Context("when cursor file contains valid JSON", func() {
			It("loads the cursor state correctly", func() {
				c := &pkg.Cursor{
					Repos: map[string]*pkg.RepoState{
						"owner/repo": {
							LastKnownState:    "red",
							CurrentEpisodeSHA: "abc123",
							DefaultBranch:     "main",
						},
					},
				}
				Expect(pkg.SaveCursor(ctx, cursorPath(), c)).To(Succeed())

				loaded, err := pkg.LoadCursor(ctx, cursorPath())
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded.Repos).To(HaveKey("owner/repo"))
				Expect(loaded.Repos["owner/repo"].LastKnownState).To(Equal("red"))
				Expect(loaded.Repos["owner/repo"].CurrentEpisodeSHA).To(Equal("abc123"))
				Expect(loaded.Repos["owner/repo"].DefaultBranch).To(Equal("main"))
			})
		})
	})

	Describe("SaveCursor + LoadCursor round-trip", func() {
		It("persists and reloads multiple repo states", func() {
			c := &pkg.Cursor{
				Repos: map[string]*pkg.RepoState{
					"owner/repo-a": {LastKnownState: "green", DefaultBranch: "main"},
					"owner/repo-b": {
						LastKnownState:    "red",
						CurrentEpisodeSHA: "sha-xyz",
						DefaultBranch:     "master",
					},
				},
			}
			Expect(pkg.SaveCursor(ctx, cursorPath(), c)).To(Succeed())

			loaded, err := pkg.LoadCursor(ctx, cursorPath())
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Repos).To(HaveLen(2))
			Expect(loaded.Repos["owner/repo-a"].LastKnownState).To(Equal("green"))
			Expect(loaded.Repos["owner/repo-b"].CurrentEpisodeSHA).To(Equal("sha-xyz"))
		})
	})

	Describe("SaveCursor Rename failure", func() {
		It("returns an error when Rename fails", func() {
			// Create a directory at cursorPath so Rename fails (cannot replace dir with file).
			cursorPath := filepath.Join(tmpDir, "cursor.json")
			Expect(os.MkdirAll(cursorPath, 0750)).To(Succeed())

			c := &pkg.Cursor{
				Repos: map[string]*pkg.RepoState{
					"owner/repo": {LastKnownState: "green"},
				},
			}
			err := pkg.SaveCursor(ctx, cursorPath, c)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("rename cursor tmp"))
		})
	})

	Describe("GetOrCreateRepoState", func() {
		It("returns an existing state for a known repo", func() {
			c := &pkg.Cursor{
				Repos: map[string]*pkg.RepoState{
					"owner/repo": {LastKnownState: "red", CurrentEpisodeSHA: "sha1"},
				},
			}
			state := pkg.GetOrCreateRepoState(c, "owner/repo")
			Expect(state.LastKnownState).To(Equal("red"))
			Expect(state.CurrentEpisodeSHA).To(Equal("sha1"))
		})

		It("inserts a zero-value entry for a new repo", func() {
			c := &pkg.Cursor{Repos: make(map[string]*pkg.RepoState)}
			state := pkg.GetOrCreateRepoState(c, "owner/new-repo")
			Expect(state).NotTo(BeNil())
			Expect(state.LastKnownState).To(Equal(""))
			Expect(state.CurrentEpisodeSHA).To(Equal(""))
			Expect(state.DefaultBranch).To(Equal(""))
			Expect(c.Repos).To(HaveKey("owner/new-repo"))
		})

		It("returns the same pointer on subsequent calls", func() {
			c := &pkg.Cursor{Repos: make(map[string]*pkg.RepoState)}
			state1 := pkg.GetOrCreateRepoState(c, "owner/repo")
			state1.LastKnownState = "green"
			state2 := pkg.GetOrCreateRepoState(c, "owner/repo")
			Expect(state2.LastKnownState).To(Equal("green"))
		})
	})
})
