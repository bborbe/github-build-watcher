// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factory wires concrete dependencies for the maintainer-watcher-github-build binary.
package factory

import (
	"context"
	"strings"

	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/cqrs/cdb"
	cqrsiam "github.com/bborbe/cqrs/iam"
	"github.com/bborbe/errors"
	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/bborbe/github-build-watcher/pkg/command"
	"github.com/bborbe/github-build-watcher/pkg/filter"
	"github.com/bborbe/github-build-watcher/pkg/handler"
	"github.com/bborbe/github-build-watcher/pkg/maintenance"
	"github.com/bborbe/github-build-watcher/pkg/wildcard"
	libkafka "github.com/bborbe/kafka"
	libkv "github.com/bborbe/kv"
	"github.com/bborbe/log"
	"github.com/bborbe/run"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"

	lib "github.com/bborbe/maintainer/lib"
)

// CreateKafkaCreateSender constructs a typed create-task command sender backed
// by a pre-built Kafka sync producer. The caller owns the producer's
// lifecycle (created in main.go so it can be reused across senders).
func CreateKafkaCreateSender(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
) task.CreateCommandSender {
	sender := cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory)
	return task.NewCreateCommandSender(sender, "")
}

// CreateSyncProducer constructs a Kafka sync producer for the
// maintainer-watcher-github-build binary. The returned cleanup closes the
// producer on shutdown; main.go owns the lifecycle and defers the cleanup
// so the producer can be reused across senders (create-task, trigger).
func CreateSyncProducer(
	ctx context.Context,
	brokers libkafka.Brokers,
) (libkafka.SyncProducer, func(), error) {
	syncProducer, err := libkafka.NewSyncProducerWithName(
		ctx,
		brokers,
		"maintainer-watcher-github-build",
	)
	if err != nil {
		return nil, nil, errors.Wrap(ctx, err, "create sync producer")
	}
	cleanup := func() {
		if err := syncProducer.Close(); err != nil {
			glog.Warningf("close kafka sync producer: %v", err)
		}
	}
	return syncProducer, cleanup, nil
}

// CreateWatcher wires all dependencies and returns a ready-to-use Watcher
// plus the Kafka sync producer it built internally (so the caller can reuse
// it for additional senders, e.g. the HTTP-side trigger sender). The cleanup
// function closes the sync producer on shutdown — the caller must defer it
// (or wire it into its own shutdown sequence).
func CreateWatcher(
	ctx context.Context,
	ghClient pkg.GitHubClient,
	brokers libkafka.Brokers,
	topicPrefix base.TopicPrefix,
	inputAllowlist []string,
	resolved pkg.AllowlistSnapshot,
	cursorPath string,
	assignee string,
	taskStatus string,
	taskPhase string,
	maxTitleLen int,
	taskSuffix string,
	currentDateTime libtime.CurrentDateTimeGetter,
) (pkg.Watcher, libkafka.SyncProducer, func(), error) {
	syncProducer, producerCleanup, err := CreateSyncProducer(ctx, brokers)
	if err != nil {
		return nil, nil, nil, errors.Wrap(ctx, err, "create sync producer")
	}
	createSender := CreateKafkaCreateSender(syncProducer, topicPrefix)
	maintenanceLoader := maintenance.NewLoader(ghClient)
	repoFilter := filter.RepoFilters{filter.NewRepoAllowlistFilter(inputAllowlist)}
	w := pkg.NewWatcher(
		ghClient,
		createSender,
		pkg.NewMetrics(),
		repoFilter,
		resolved,
		cursorPath,
		assignee,
		taskStatus,
		taskPhase,
		maintenanceLoader,
		maxTitleLen,
		taskSuffix,
		currentDateTime,
	)
	return w, syncProducer, producerCleanup, nil
}

// CreateTriggerBuildCheckCommandSender constructs a typed trigger-build-check
// command sender backed by a Kafka sync producer. This is the HTTP-side
// sender: the /trigger handler publishes TriggerBuildCheckCommand messages
// through it.
//
// CommandCreator and Initiator are built once here and reused across every
// SendCommand call (per cqrs/docs/producing-commands.md "Factory Wiring";
// matches trading/frontend/command's reference impl).
func CreateTriggerBuildCheckCommandSender(
	ctx context.Context,
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
) command.TriggerBuildCheckCommandSender {
	return command.NewTriggerBuildCheckCommandSender(
		base.NewCommandCreator(base.RequestIDChannel(ctx)),
		cqrsiam.Initiator("watcher-github-build"),
		cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory),
	)
}

// CreateTriggerBuildCheckHandler wires the thin CQRS handler that publishes a
// TriggerBuildCheckCommand to Kafka for each /trigger request.
// All poll-cycle work lives in the in-pod command consumer (see
// pkg/command.NewTriggerBuildCheckCommandExecutor).
func CreateTriggerBuildCheckHandler(
	sender command.TriggerBuildCheckCommandSender,
) handler.TriggerBuildCheckHandler {
	return handler.NewTriggerBuildCheckHandler(sender)
}

// CreateCommandConsumer wires a run.Func that consumes TriggerBuildCheckCommand
// messages from the github-build watcher's request topic (identified by
// lib.GithubBuildV1SchemaID) and runs them through the shared
// Watcher.Poll(ctx) pipeline.
//
// The function is pure composition: no business logic, no conditionals.
// It uses cdb.RunCommandConsumerTxDefault (auto-wraps the transaction) per
// the go-cqrs/auto-tx-wrapper-no-manual-wrap rule — do NOT manually wrap
// the executor with kv.NewTransactionMiddleware.
func CreateCommandConsumer(
	saramaClientProvider libkafka.SaramaClientProvider,
	syncProducer libkafka.SyncProducer,
	db libkv.DB,
	watcher pkg.Watcher,
	topicPrefix base.TopicPrefix,
) run.Func {
	executors := cdb.CommandObjectExecutorTxs{
		command.NewTriggerBuildCheckCommandExecutor(watcher),
	}
	return cdb.RunCommandConsumerTxDefault(
		saramaClientProvider,
		syncProducer,
		db,
		lib.GithubBuildV1SchemaID,
		topicPrefix,
		false, // ignoreUnsupported
		executors,
	)
}

// countWildcards returns the number of wildcard entries (host/owner/*) in the list.
func countWildcards(entries []string) int {
	n := 0
	for _, e := range entries {
		parts := strings.Split(strings.TrimSpace(e), "/")
		if len(parts) == 3 && parts[2] == "*" {
			n++
		}
	}
	return n
}

// CreateAllowlistSnapshot returns a snapshot provider and (optionally) a background
// refresh task for the daemon's run loop.
// If the input allowlist contains wildcards, a ResolvedAllowlist with a refresh goroutine
// is returned. Otherwise, a static snapshot with no background refresh is returned.
func CreateAllowlistSnapshot(
	ghClient pkg.GitHubClient,
	repoAllowlist []string,
) (pkg.AllowlistSnapshot, run.Func, error) {
	if wildcard.HasWildcard(repoAllowlist) {
		expander := wildcard.NewExpander(ghClient)
		resolvedSet := wildcard.NewResolvedAllowlist(expander, repoAllowlist)
		glog.V(2).Infof(
			"wildcard_refresh_enabled entries=%d (interval=%s)",
			countWildcards(repoAllowlist), wildcard.RefreshInterval(),
		)
		return resolvedSet, func(ctx context.Context) error {
			defer func() {
				if rec := recover(); rec != nil {
					glog.Errorf("wildcard refresh loop panic recovered: %v", rec)
				}
			}()
			return resolvedSet.RunRefreshLoop(ctx)
		}, nil
	}
	glog.V(2).Infof("wildcard_refresh_disabled allowlist=pure-literal")
	return pkg.NewStaticSnapshot(repoAllowlist), nil, nil
}
