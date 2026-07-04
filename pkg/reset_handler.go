// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"net/http"

	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	"github.com/golang/glog"
	"github.com/gorilla/mux"
)

// NewResetCursorHandler returns an HTTP handler that clears the cursor entry
// for one repo. Repo is taken from the {repo} URL variable (e.g.
// "github.com/bborbe/maintainer"). Releases an episode lock so the next poll
// detects the build state from scratch and may publish a fresh create-task.
//
// Wrap with libhttp.NewDangerousHandlerWrapper at the call site to require a
// passphrase — the bare handler does not enforce auth.
//
// Race: a concurrent Poll may overwrite the reset; operator should retry if
// the next poll log doesn't show a state transition for the target repo.
func NewResetCursorHandler(cursorPath string) http.Handler {
	return libhttp.NewErrorHandler(
		libhttp.WithErrorFunc(
			func(ctx context.Context, resp http.ResponseWriter, req *http.Request) error {
				repoKey := mux.Vars(req)["repo"]
				if repoKey == "" {
					return libhttp.WrapWithStatusCode(
						errors.Errorf(ctx, "missing {repo} path variable"),
						http.StatusBadRequest,
					)
				}

				cursor, err := LoadCursor(ctx, cursorPath)
				if err != nil {
					return errors.Wrapf(ctx, err, "load cursor for reset")
				}
				if _, ok := cursor.Repos[repoKey]; !ok {
					return libhttp.WrapWithStatusCode(
						errors.Errorf(ctx, "repo not found in cursor: %s", repoKey),
						http.StatusNotFound,
					)
				}
				delete(cursor.Repos, repoKey)
				if err := SaveCursor(ctx, cursorPath, cursor); err != nil {
					return errors.Wrapf(ctx, err, "save cursor after reset")
				}
				glog.Warningf("cursor reset for repo=%s", repoKey)
				_, _ = libhttp.WriteAndGlog(resp, "cursor reset for "+repoKey)
				return nil
			},
		),
	)
}
