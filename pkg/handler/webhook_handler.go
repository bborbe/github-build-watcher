// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/bborbe/github-build-watcher/pkg/command"
	libhttp "github.com/bborbe/http"
	libtime "github.com/bborbe/time"
	"github.com/golang/glog"
)

// WebhookHeader is a typed GitHub webhook header name, so header-name typos
// are caught by the type system.
type WebhookHeader string

const (
	WebhookSignatureHeader WebhookHeader = "X-Hub-Signature-256"
	WebhookEventHeader     WebhookHeader = "X-GitHub-Event"
)

// AvailableWebhookHeaders lists the webhook header names the handler reads.
var AvailableWebhookHeaders = []WebhookHeader{WebhookSignatureHeader, WebhookEventHeader}

//counterfeiter:generate -o ../../mocks/webhook_metrics.go --fake-name WebhookMetrics . WebhookMetrics

// WebhookMetrics is the narrow slice of watcher metrics the webhook handler
// records. pkg.Metrics satisfies it structurally, keeping this handler free
// of the pkg import — mirroring trigger_handler's thin dependency set.
type WebhookMetrics interface {
	IncWebhookDelivery(result string)
	IncWebhookSignatureRejected()
	ObserveWebhookDispatchLatency(seconds float64)
	// IncWebhookSkipped counts workflow_run deliveries that did not dispatch a
	// build-check. reason: "not_completed" | "not_failure" | "not_default_branch" | "debounced".
	IncWebhookSkipped(reason string)
}

// WebhookHandler handles POST /webhook/github-build.
// The handler is intentionally thin, mirroring TriggerBuildCheckHandler:
// verify the GitHub HMAC signature, extract the repo from the workflow_run
// event, publish a TriggerBuildCheckCommand to Kafka, and return 202. All
// GitHub API access, filter evaluation, and state-machine logic stays in the
// in-pod command consumer (shared with /trigger), so the allowlist gate and
// the green/red state transitions apply to webhook deliveries unchanged.
type WebhookHandler = libhttp.WithError

// NewWebhookHandler returns a handler that publishes a TriggerBuildCheckCommand
// for each signature-verified workflow_run webhook delivery that (a) completed
// with conclusion=failure, (b) ran on the repository's default branch, and (c)
// passes the per-repo debounce. All other deliveries are acked and skipped.
func NewWebhookHandler(
	sender command.TriggerBuildCheckCommandSender,
	secret string,
	metrics WebhookMetrics,
	clock libtime.CurrentDateTimeGetter,
	debouncer pkg.Debouncer,
) WebhookHandler {
	return &webhookHandler{
		sender:    sender,
		secret:    secret,
		metrics:   metrics,
		clock:     clock,
		debouncer: debouncer,
	}
}

type webhookHandler struct {
	sender    command.TriggerBuildCheckCommandSender
	secret    string
	metrics   WebhookMetrics
	clock     libtime.CurrentDateTimeGetter
	debouncer pkg.Debouncer
}

func (h *webhookHandler) ServeHTTP(
	ctx context.Context,
	resp http.ResponseWriter,
	req *http.Request,
) error {
	start := h.clock.Now()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return libhttp.WrapWithStatusCode(
			errors.Wrap(ctx, err, "read webhook body"),
			http.StatusBadRequest,
		)
	}
	if err := verifyWebhookSignature(
		ctx,
		h.secret,
		req.Header.Get(string(WebhookSignatureHeader)),
		body,
	); err != nil {
		h.metrics.IncWebhookSignatureRejected()
		return libhttp.WrapWithStatusCode(err, http.StatusUnauthorized)
	}

	event := req.Header.Get(string(WebhookEventHeader))
	if event == "ping" {
		return writeWebhookAck(resp)
	}
	if event != "workflow_run" {
		glog.V(2).Infof("webhook ignored event=%s", event)
		return writeWebhookAck(resp)
	}

	var payload webhookWorkflowRunEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return libhttp.WrapWithStatusCode(
			errors.Wrap(ctx, err, "parse webhook payload"),
			http.StatusBadRequest,
		)
	}
	if payload.Repository.FullName == "" {
		return libhttp.WrapWithStatusCode(
			errors.Errorf(ctx, "webhook payload missing repository.full_name"),
			http.StatusBadRequest,
		)
	}

	// Only a completed/failure run on the default branch can change the build
	// state; everything else is acked and skipped (no build-check storm).
	if reason := webhookSkipReason(payload); reason != "" {
		h.metrics.IncWebhookSkipped(reason)
		glog.V(2).Infof(
			"webhook skipped repo=%s reason=%s",
			payload.Repository.FullName, reason,
		)
		return writeWebhookAck(resp)
	}

	// Per-repo debounce: a burst of completed-failure events for the same repo
	// collapses to one dispatch within the min interval.
	if !h.debouncer.Allow(payload.Repository.FullName) {
		h.metrics.IncWebhookSkipped("debounced")
		glog.V(2).Infof(
			"webhook skipped repo=%s reason=debounced",
			payload.Repository.FullName,
		)
		return writeWebhookAck(resp)
	}

	// Dispatch the repo-scoped payload — the executor's Poll narrows the scan
	// to this single repo (scoped poll), ~3 API calls not a fleet scan.
	if err := h.sender.SendCommand(ctx, command.TriggerBuildCheckCommand{
		Scope: payload.Repository.FullName,
	}); err != nil {
		return libhttp.WrapWithStatusCode(
			errors.Wrap(ctx, err, "send TriggerBuildCheckCommand"),
			http.StatusBadGateway,
		)
	}

	h.metrics.IncWebhookDelivery("success")
	h.metrics.ObserveWebhookDispatchLatency(h.clock.Now().Sub(start).Duration().Seconds())
	glog.V(2).Infof(
		"webhook accepted repo=%s branch=%s conclusion=%s",
		payload.Repository.FullName, payload.WorkflowRun.HeadBranch, payload.WorkflowRun.Conclusion,
	)
	return writeWebhookDispatched(resp)
}

// webhookWorkflowRunEvent is the subset of a GitHub workflow_run event the
// handler reads: the action, the run's conclusion and head branch, and the
// repository's full name + default branch (used to gate default-branch runs).
type webhookWorkflowRunEvent struct {
	Action string `json:"action"`
	// WorkflowRun carries the conclusion + head branch of the run that just
	// changed state.
	WorkflowRun struct {
		Conclusion string `json:"conclusion"`
		HeadBranch string `json:"head_branch"`
	} `json:"workflow_run"`
	// Repository carries the repo identity + its default branch.
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
}

// webhookSkipReason returns the skip reason for a workflow_run delivery that
// should not dispatch a build-check, or "" when the delivery dispatches. A
// busy CI fleet fires workflow_run events for every step/check on every branch;
// only a run that completed with conclusion=failure on the repository's default
// branch can change the build state, so everything else is acked and skipped.
func webhookSkipReason(p webhookWorkflowRunEvent) string {
	switch {
	case p.Action != "completed":
		return "not_completed"
	case p.WorkflowRun.Conclusion != "failure":
		return "not_failure"
	case p.WorkflowRun.HeadBranch != p.Repository.DefaultBranch:
		return "not_default_branch"
	}
	return ""
}

// verifyWebhookSignature checks the X-Hub-Signature-256 header ("sha256=<hex>")
// against an HMAC-SHA256 of the raw body, in constant time. An empty configured
// secret rejects everything (fail closed).
func verifyWebhookSignature(
	ctx context.Context,
	secret string,
	provided string,
	body []byte,
) error {
	if secret == "" {
		return errors.Errorf(ctx, "webhook secret not configured")
	}
	if provided == "" {
		return errors.Errorf(ctx, "missing webhook signature header")
	}
	_, sigHex, found := strings.Cut(provided, "=")
	if !found || sigHex == "" {
		return errors.Errorf(ctx, "malformed webhook signature header")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sigHex)) {
		return errors.Errorf(ctx, "invalid webhook signature")
	}
	return nil
}

// writeWebhookAck acknowledges a handled-but-not-dispatched delivery (ping,
// unsupported event, non-failing run, non-default-branch run) with 200 so
// GitHub does not retry.
func writeWebhookAck(resp http.ResponseWriter) error {
	resp.WriteHeader(http.StatusOK)
	return nil
}

// writeWebhookDispatched returns 202 with {"status":"accepted"} once the
// TriggerBuildCheckCommand has been published to Kafka.
func writeWebhookDispatched(resp http.ResponseWriter) error {
	resp.Header().Set("Content-Type", "application/json")
	resp.WriteHeader(http.StatusAccepted)
	return json.NewEncoder(resp).Encode(map[string]string{"status": "accepted"})
}
