// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package auth resolves a GitHub App HTTP client for the watcher binaries.
package auth

import (
	"context"
	"net/http"
	"os"

	"github.com/bborbe/errors"
	"github.com/golang/glog"

	githubapp "github.com/bborbe/maintainer/githubapp"
)

// Config is the resolver input.
type Config struct {
	AppID          int64
	InstallationID int64
	PEMKeyFile     string
	PEMKey         string
	LogPrefix      string // e.g. "watcher/github-build" or "watcher/github-build-run-once"
}

// Resolve picks GitHub App auth and returns an *http.Client suitable for
// go-github. The App-mode client has an auto-refreshing transport
// (lib/githubapp.NewClient).
func Resolve(ctx context.Context, cfg Config) (*http.Client, error) {
	hasPEMFile := cfg.PEMKeyFile != ""
	hasPEMContent := cfg.PEMKey != ""
	useGitHubApp := cfg.AppID != 0 && cfg.InstallationID != 0 && (hasPEMFile || hasPEMContent)
	if !useGitHubApp {
		return nil, errors.Errorf(
			ctx,
			"%s auth: GitHub App credentials not configured — set APP_ID, INSTALLATION_ID, and PEM_KEY_FILE (or PEM_KEY)",
			cfg.LogPrefix,
		)
	}
	appCfg := githubapp.Config{AppID: cfg.AppID, InstallationID: cfg.InstallationID}
	if hasPEMFile {
		pemBytes, err := os.ReadFile(cfg.PEMKeyFile)
		if err != nil {
			return nil, errors.Wrapf(ctx, err, "read PEM file %s", cfg.PEMKeyFile)
		}
		appCfg.PEM = pemBytes
	} else {
		appCfg.PEM = []byte(cfg.PEMKey)
	}
	httpClient, err := githubapp.NewClient(ctx, appCfg)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "create githubapp client")
	}
	glog.V(2).Infof("%s auth mode=github-app app_id=%d installation_id=%d",
		cfg.LogPrefix, cfg.AppID, cfg.InstallationID)
	return httpClient, nil
}
