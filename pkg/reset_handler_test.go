// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/gorilla/mux"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ResetCursorHandler", func() {
	var ctx context.Context
	var tmpDir string
	var cursorPath string

	BeforeEach(func() {
		ctx = context.Background()
		tmpDir = GinkgoT().TempDir()
		cursorPath = filepath.Join(tmpDir, "cursor.json")
	})

	buildRouter := func() *mux.Router {
		router := mux.NewRouter()
		router.Path("/resetcursor/{repo:.+}").Handler(pkg.NewResetCursorHandler(cursorPath))
		return router
	}

	writeStarterCursor := func(repos map[string]*pkg.RepoState) {
		c := &pkg.Cursor{Repos: repos}
		Expect(pkg.SaveCursor(ctx, cursorPath, c)).To(Succeed())
	}

	Describe("successful reset", func() {
		It("removes the repo entry from cursor and returns 200 with the repo key in body", func() {
			writeStarterCursor(map[string]*pkg.RepoState{
				"github.com/bborbe/foo": {LastKnownState: "red", CurrentEpisodeSHA: "sha-abc"},
				"github.com/bborbe/bar": {LastKnownState: "green"},
			})

			router := buildRouter()
			req := httptest.NewRequest(http.MethodPost, "/resetcursor/github.com/bborbe/foo", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(rec.Body.String()).To(ContainSubstring("github.com/bborbe/foo"))

			loaded, err := pkg.LoadCursor(ctx, cursorPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.Repos).NotTo(HaveKey("github.com/bborbe/foo"))
			Expect(loaded.Repos).To(HaveKey("github.com/bborbe/bar"))
		})

		It("accepts repo keys containing slashes via {repo:.+} regex", func() {
			writeStarterCursor(map[string]*pkg.RepoState{
				"github.com/owner/repo": {LastKnownState: "red"},
			})

			router := buildRouter()
			req := httptest.NewRequest(http.MethodPost, "/resetcursor/github.com/owner/repo", nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Describe("unknown repo", func() {
		It("returns 404 when repo is not in cursor", func() {
			writeStarterCursor(map[string]*pkg.RepoState{
				"github.com/bborbe/other": {LastKnownState: "green"},
			})

			router := buildRouter()
			req := httptest.NewRequest(
				http.MethodPost,
				"/resetcursor/github.com/bborbe/unknown",
				nil,
			)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotFound))
			Expect(rec.Body.String()).To(ContainSubstring("repo not found in cursor"))
		})
	})
})
