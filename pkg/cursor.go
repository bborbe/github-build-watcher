// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"os"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// DefaultCursorPath is the default path for cursor state persistence.
const DefaultCursorPath = "/data/cursor.json"

// RepoState holds the persisted build state for one repository.
type RepoState struct {
	LastKnownState    string `json:"last_known_state"`    // "green" | "red" | ""
	CurrentEpisodeSHA string `json:"current_episode_sha"` // empty when green
	DefaultBranch     string `json:"default_branch"`      // cached; fetched via API if empty
}

// Cursor is the full persisted state for the build watcher.
type Cursor struct {
	Repos map[string]*RepoState `json:"repos"` // key: "owner/repo"
}

// LoadCursor reads cursor state from path.
// Missing file returns a fresh empty cursor (cold start is valid).
// Corrupt file returns an error; caller should refuse to proceed.
func LoadCursor(ctx context.Context, path string) (*Cursor, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is config-controlled
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("cursor file not found, cold-start path=%s", path)
			return &Cursor{Repos: make(map[string]*RepoState)}, nil
		}
		return nil, errors.Wrapf(ctx, err, "read cursor file path=%s", path)
	}
	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, errors.Wrapf(ctx, err, "unmarshal cursor file path=%s", path)
	}
	if c.Repos == nil {
		c.Repos = make(map[string]*RepoState)
	}
	return &c, nil
}

// SaveCursor persists cursor state to path atomically via a temp file + rename.
// On error: logs warning and returns error; caller logs and continues.
func SaveCursor(ctx context.Context, path string, c *Cursor) error {
	data, err := json.Marshal(c)
	if err != nil {
		return errors.Wrapf(ctx, err, "marshal cursor state path=%s", path)
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil { // #nosec G306 -- intentional 0600
		return errors.Wrapf(ctx, err, "write cursor tmp path=%s", path)
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return errors.Wrapf(ctx, err, "rename cursor tmp path=%s", path)
	}
	return nil
}

// GetOrCreateRepoState returns the existing state for key or inserts a new zero-value RepoState.
// A zero-value LastKnownState ("") is treated as green by the state machine.
func GetOrCreateRepoState(c *Cursor, key string) *RepoState {
	if state, ok := c.Repos[key]; ok {
		return state
	}
	state := &RepoState{}
	c.Repos[key] = state
	return state
}
