// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package maintenance

import (
	"context"

	"github.com/golang/glog"
	"gopkg.in/yaml.v3"
)

// FileContentFetcher fetches raw file bytes from a GitHub repository.
// Matches the GetFileContent method signature on pkg.GitHubClient.
//
//counterfeiter:generate -o ../../mocks/file_content_fetcher.go --fake-name FileContentFetcher . FileContentFetcher
type FileContentFetcher interface {
	GetFileContent(ctx context.Context, owner, repo, path, ref string) ([]byte, error)
}

// GithubBuildConfig holds the watcher.github-build subtree of .maintenance.yaml.
// All fields are optional; empty string means "no override — use watcher default".
type GithubBuildConfig struct {
	Assignee    string
	Status      string
	Phase       string
	IncludeLogs bool // include_logs: true enables the ## Error log snippet opt-in
}

// Loader fetches per-repo override config for the build watcher.
//
//counterfeiter:generate -o ../../mocks/maintenance_loader.go --fake-name MaintenanceLoader . Loader
type Loader interface {
	// LoadOverrides fetches .maintenance.yaml from the repo's default branch and
	// returns the watcher.github-build subtree. Never returns an error — all
	// failures are logged and result in an empty GithubBuildConfig (fall through
	// to watcher defaults). Empty string fields mean "no override".
	LoadOverrides(ctx context.Context, owner, repo, defaultBranch string) GithubBuildConfig
}

// NewLoader returns a Loader backed by the given FileContentFetcher.
func NewLoader(fetcher FileContentFetcher) Loader {
	return &loaderImpl{fetcher: fetcher}
}

type loaderImpl struct {
	fetcher FileContentFetcher
}

// rawConfig is the full .maintenance.yaml structure used for YAML unmarshalling.
type rawConfig struct {
	Watcher map[string]map[string]interface{} `yaml:"watcher"`
}

func (l *loaderImpl) LoadOverrides(
	ctx context.Context,
	owner, repo, defaultBranch string,
) GithubBuildConfig {
	filePath := ".maintenance.yaml"
	content, err := l.fetcher.GetFileContent(ctx, owner, repo, filePath, defaultBranch)
	if err != nil {
		glog.Warningf("maintenance loader: fetch failed owner=%s repo=%s err=%v", owner, repo, err)
		return GithubBuildConfig{}
	}
	if content == nil {
		// 404 — file absent is the common case; no log
		return GithubBuildConfig{}
	}

	var raw rawConfig
	if err := yaml.Unmarshal(content, &raw); err != nil {
		glog.Warningf(
			"maintenance loader: malformed YAML owner=%s repo=%s path=%s err=%v",
			owner,
			repo,
			filePath,
			err,
		)
		return GithubBuildConfig{}
	}

	watcherSection := raw.Watcher
	if watcherSection == nil {
		return GithubBuildConfig{}
	}
	buildSection, ok := watcherSection["github-build"]
	if !ok || buildSection == nil {
		return GithubBuildConfig{}
	}

	// Log INFO for unknown keys; extract known keys.
	known := map[string]bool{"assignee": true, "status": true, "phase": true, "include_logs": true}
	for k := range buildSection {
		if !known[k] {
			glog.Infof(
				"maintenance loader: ignored unknown key watcher.github-build.%s in %s/%s/%s",
				k,
				owner,
				repo,
				filePath,
			)
		}
	}

	cfg := GithubBuildConfig{}
	if v, ok := buildSection["assignee"].(string); ok {
		cfg.Assignee = v
	}
	if v, ok := buildSection["status"].(string); ok {
		cfg.Status = v
	}
	if v, ok := buildSection["phase"].(string); ok {
		cfg.Phase = v
	}
	if v, ok := buildSection["include_logs"].(bool); ok {
		cfg.IncludeLogs = v
	}
	return cfg
}
