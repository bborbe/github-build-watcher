// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"strings"

	"github.com/golang/glog"
)

// DefaultMaxTitleLen is the default safety cap for build-failure filenames. Override via MAX_TITLE_LEN.
const DefaultMaxTitleLen = 200

// computeBuildTitle returns the human-readable title for a build-failure task.
// Format: "Build Failure {provider} - {slugifySegment(owner)}-{slugifySegment(repo)} - {sha7}"
// When taskSuffix is non-empty, appends " - <suffix>" (before maxTitle cap if needed).
// The returned string MUST NOT include the .md extension; the controller appends it.
func computeBuildTitle(
	provider, owner, repo, episodeSHA string,
	maxTitle int,
	taskSuffix string,
) string {
	sha7 := episodeSHA
	if len(sha7) > 7 {
		sha7 = sha7[:7]
	}
	ownerRepo := slugifySegment(owner) + "-" + slugifySegment(repo)
	title := "Build Failure " + provider + " - " + ownerRepo + " - " + sha7
	var suffixPart string
	if taskSuffix != "" {
		suffixPart = " - " + taskSuffix
	}
	if len(title)+len(suffixPart) > maxTitle {
		glog.Warningf(
			"build task title exceeds max length: len=%d max=%d suffix=%q — truncating to preserve suffix",
			len(title)+len(suffixPart),
			maxTitle,
			taskSuffix,
		)
		budget := maxTitle - len(suffixPart)
		if budget < 0 {
			budget = 0
		}
		if len(title) > budget {
			title = title[:budget]
		}
	}
	return title + suffixPart
}

// slugifySegment converts s to a filesystem-safe lowercase segment.
// Non-[a-z0-9] characters (including uppercase letters) are replaced with hyphens;
// leading and trailing hyphens are stripped.
func slugifySegment(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
