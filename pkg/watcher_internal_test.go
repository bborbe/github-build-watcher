// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("splitRepoKey", func() {
	DescribeTable("parses allowlist key into owner and repo",
		func(key, wantOwner, wantRepo string) {
			gotOwner, gotRepo := splitRepoKey(key)
			Expect(gotOwner).To(Equal(wantOwner))
			Expect(gotRepo).To(Equal(wantRepo))
		},
		Entry("three-segment host/owner/repo", "github.com/owner/repo", "owner", "repo"),
		Entry("two-segment owner/repo", "owner/repo", "owner", "repo"),
		Entry("single segment", "single", "single", ""),
		Entry("empty string", "", "", ""),
		Entry("four segments (invalid)", "a/b/c/d", "a/b/c/d", ""),
	)
})

var _ = Describe("computeBuildTitle", func() {
	DescribeTable(
		"produces correct title",
		func(provider, owner, repo, sha string, maxTitle int, taskSuffix, want string) {
			Expect(
				computeBuildTitle(provider, owner, repo, sha, maxTitle, taskSuffix),
			).To(Equal(want))
		},
		Entry(
			"normal github repo",
			"github",
			"bborbe",
			"maintainer",
			"5886450abcdef",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - bborbe-maintainer - 5886450",
		),
		Entry(
			"sha shorter than 7 chars",
			"github",
			"org",
			"repo",
			"abc12",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - org-repo - abc12",
		),
		Entry(
			"sha exactly 7 chars",
			"github",
			"org",
			"repo",
			"abc1234",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - org-repo - abc1234",
		),
		Entry(
			"sha longer than 7 chars is truncated to 7",
			"github",
			"org",
			"repo",
			"abc1234xyz",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - org-repo - abc1234",
		),
		Entry(
			"repo name with uppercase is slugified",
			"github",
			"MyOrg",
			"MyRepo",
			"abcdef0",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - myorg-myrepo - abcdef0",
		),
		Entry(
			"repo name with dot is slugified",
			"github",
			"org",
			"my.repo",
			"abcdef0",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - org-my-repo - abcdef0",
		),
		Entry(
			"repo name with colon (illegal on vault fs) is slugified",
			"github",
			"org",
			"my:repo",
			"abcdef0",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - org-my-repo - abcdef0",
		),
		Entry(
			"hyphenated names preserved",
			"github",
			"my-org",
			"my-repo",
			"abcdef0",
			DefaultMaxTitleLen,
			"",
			"Build Failure github - my-org-my-repo - abcdef0",
		),
		Entry(
			"future bitbucket provider",
			"bitbucket",
			"team",
			"svc",
			"a1b2c3d",
			DefaultMaxTitleLen,
			"",
			"Build Failure bitbucket - team-svc - a1b2c3d",
		),
		Entry(
			"custom maxTitle truncates long title",
			"github",
			"bborbe",
			"maintainer",
			"5886450abcdef",
			40,
			"",
			"Build Failure github - bborbe-maintainer",
		),
		Entry(
			"custom maxTitle larger than title leaves it unchanged",
			"github",
			"org",
			"repo",
			"abc1234",
			200,
			"",
			"Build Failure github - org-repo - abc1234",
		),
	)
})

var _ = Describe("slugifySegment", func() {
	DescribeTable("produces filesystem-safe segment",
		func(input, want string) {
			Expect(slugifySegment(input)).To(Equal(want))
		},
		Entry("already safe lowercase", "bborbe", "bborbe"),
		Entry("uppercase converted", "MyOrg", "myorg"),
		Entry("dot replaced with hyphen", "my.repo", "my-repo"),
		Entry("colon replaced with hyphen", "my:repo", "my-repo"),
		Entry("asterisk replaced with hyphen", "my*repo", "my-repo"),
		Entry("leading special char stripped", ".leading", "leading"),
		Entry("trailing special char stripped", "trailing.", "trailing"),
		Entry("only special chars becomes empty", ":::", ""),
		Entry("mixed chars", "My.Org_1", "my-org-1"),
		Entry("digits preserved", "repo2", "repo2"),
		Entry("hyphen preserved", "my-repo", "my-repo"),
	)
})

// Wire-format contract: lock "title" in and "filename_hint" out.
var _ = Describe("task.CreateCommand wire format", func() {
	It("emits 'title' as the top-level key (not 'filename_hint')", func() {
		cmd := task.CreateCommand{
			Title:          "Build Failure github - bborbe-maintainer - 5886450",
			TaskIdentifier: agentlib.TaskIdentifier("00000000-0000-0000-0000-000000000000"),
			Frontmatter:    agentlib.TaskFrontmatter{"assignee": "bborbe"},
			Body:           "# body",
		}
		raw, err := json.Marshal(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(
			string(raw),
		).To(ContainSubstring(`"title":"Build Failure github - bborbe-maintainer - 5886450"`))
		Expect(string(raw)).NotTo(ContainSubstring(`"filename_hint"`))
	})

	// Boundary contract: slug helper output MUST pass task.CreateCommand.Validate.
	// Prevents future drift between watcher's slug rules and lib's Title validator.
	DescribeTable("computeBuildTitle output passes task.CreateCommand.Validate",
		func(provider, owner, repo, sha string) {
			title := computeBuildTitle(provider, owner, repo, sha, DefaultMaxTitleLen, "")
			cmd := task.CreateCommand{
				TaskIdentifier: agentlib.TaskIdentifier("00000000-0000-0000-0000-000000000000"),
				Title:          title,
				Frontmatter: agentlib.TaskFrontmatter{
					"assignee": "build-fixer-agent",
					"status":   "todo",
				},
				Body: "build failed",
			}
			Expect(cmd.Validate(context.Background())).To(Succeed())
		},
		Entry("typical", "github", "bborbe", "maintainer", "5886450a1234"),
		Entry("hyphenated repo", "github", "my-org", "my-repo", "abc1234"),
		Entry("digits in repo", "github", "bborbe", "repo123", "deadbeef"),
	)
})

var _ = Describe("formatDuration", func() {
	DescribeTable("formats duration as human-readable string",
		func(input time.Duration, want string) {
			Expect(formatDuration(input)).To(Equal(want))
		},
		Entry("zero duration returns empty", 0*time.Second, ""),
		Entry("negative duration returns empty", -1*time.Second, ""),
		Entry("seconds only", 47*time.Second, "47s"),
		Entry("minutes and seconds", 2*time.Minute+47*time.Second, "2m 47s"),
		Entry("hours, minutes, seconds", 1*time.Hour+5*time.Minute+3*time.Second, "1h 5m 3s"),
		Entry("exactly one minute", 60*time.Second, "1m 0s"),
		Entry("sub-500ms rounds to zero → empty", 499*time.Millisecond, ""),
		Entry("exactly 500ms rounds to 1s", 500*time.Millisecond, "1s"),
		Entry("1499ms rounds to 1s", 1499*time.Millisecond, "1s"),
		Entry("1500ms rounds to 2s", 1500*time.Millisecond, "2s"),
	)
})

var _ = Describe("redactLogSnippet", func() {
	DescribeTable("redacts known secret patterns",
		func(input, wantContain, wantNotContain string) {
			result := redactLogSnippet(input)
			if wantContain != "" {
				Expect(result).To(ContainSubstring(wantContain))
			}
			if wantNotContain != "" {
				Expect(result).NotTo(ContainSubstring(wantNotContain))
			}
		},
		Entry("GitHub PAT (ghp_)",
			"token=ghp_ABCDEFGHIJKLMNOPabcde",
			"[REDACTED]", "ghp_ABCDEFGHIJKLMNOPabcde"),
		Entry("GitHub OAuth token (gho_)",
			"Authorization: gho_ABCDEFGHIJKLMNOPqrstu",
			"[REDACTED]", "gho_ABCDEFGHIJKLMNOPqrstu"),
		Entry("Bearer header",
			"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc",
			"Bearer [REDACTED]", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"),
		Entry("AWS access key ID",
			"access_key=AKIAIOSFODNN7EXAMPLE1",
			"[REDACTED]", "AKIAIOSFODNN7EXAMPLE1"),
		Entry("AWS secret access key",
			"aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"aws_secret_access_key = [REDACTED]", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
		Entry("long hex string (≥40 hex chars)",
			"token: da39a3ee5e6b4b0d3255bfef95601890afd80709 ok",
			"[REDACTED]", "da39a3ee5e6b4b0d3255bfef95601890afd80709"),
		Entry("safe short hex string not redacted",
			"short: abc123",
			"short: abc123", ""),
		Entry("non-secret text passes through unchanged",
			"INFO: build succeeded in 42s",
			"INFO: build succeeded in 42s", ""),
	)
})

var _ = Describe("lastNLinesUpTo4KB", func() {
	It("returns last N lines when fewer than N lines exist", func() {
		Expect(lastNLinesUpTo4KB("a\nb\nc", 10)).To(Equal("a\nb\nc"))
	})

	It("returns exactly the last N lines when more exist", func() {
		input := strings.Join([]string{"1", "2", "3", "4", "5"}, "\n")
		result := lastNLinesUpTo4KB(input, 3)
		Expect(result).To(Equal("3\n4\n5"))
	})

	It("caps at 4096 bytes when last N lines exceed 4 KB", func() {
		// Build a string where the last 30 lines sum to > 4096 bytes
		longLine := strings.Repeat("x", 200) // 200 bytes per line
		lines := make([]string, 40)
		for i := range lines {
			lines[i] = longLine
		}
		input := strings.Join(lines, "\n")
		result := lastNLinesUpTo4KB(input, 30)
		Expect(len(result)).To(BeNumerically("<=", 4096))
	})

	It("handles empty input", func() {
		Expect(lastNLinesUpTo4KB("", 30)).To(Equal(""))
	})
})
