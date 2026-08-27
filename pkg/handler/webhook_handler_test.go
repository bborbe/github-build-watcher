// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/bborbe/github-build-watcher/mocks"
	"github.com/bborbe/github-build-watcher/pkg"
	"github.com/bborbe/github-build-watcher/pkg/handler"
	libhttp "github.com/bborbe/http"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const webhookTestSecret = "test-secret"

// signBody produces the X-Hub-Signature-256 value GitHub would send for a body.
func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

var _ = Describe("WebhookHandler", func() {
	var (
		ctx     context.Context
		sender  *mocks.TriggerBuildCheckCommandSender
		metrics *mocks.WebhookMetrics
		h       http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		sender = new(mocks.TriggerBuildCheckCommandSender)
		metrics = new(mocks.WebhookMetrics)
		h = libhttp.NewErrorHandler(
			handler.NewWebhookHandler(
				sender,
				webhookTestSecret,
				metrics,
				libtime.NewCurrentDateTime(),
				pkg.NewDebouncer(time.Second, libtime.NewCurrentDateTime()),
			),
		)
	})

	// webhookRequest builds a signed POST /webhook/github-build with the given
	// GitHub event and raw payload body.
	webhookRequest := func(event, payload string) *http.Request {
		req := httptest.NewRequest("POST", "/webhook/github-build", strings.NewReader(payload))
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-Hub-Signature-256", signBody(webhookTestSecret, []byte(payload)))
		return req
	}

	// workflowRunPayload builds a workflow_run event that completed with
	// conclusion=failure on the repo's default branch (the dispatch case).
	workflowRunPayload := func(repo string) string {
		return `{"action":"completed","workflow_run":{"conclusion":"failure","head_branch":"master"},"repository":{"full_name":"` + repo + `","default_branch":"master"}}`
	}

	// workflowRunPayloadOnBranch builds a workflow_run on the given head branch
	// (used to exercise the default-branch gate).
	workflowRunPayloadOnBranch := func(repo, headBranch, defaultBranch string) string {
		return `{"action":"completed","workflow_run":{"conclusion":"failure","head_branch":"` + headBranch + `"},"repository":{"full_name":"` + repo + `","default_branch":"` + defaultBranch + `"}}`
	}

	Context("signature verification", func() {
		It(
			"rejects a missing signature with 401, no publish, increments rejection counter",
			func() {
				req := httptest.NewRequest(
					"POST",
					"/webhook/github-build",
					strings.NewReader(workflowRunPayload("bborbe/repo")),
				)
				req.Header.Set("X-GitHub-Event", "workflow_run")
				resp := httptest.NewRecorder()
				h.ServeHTTP(resp, req)

				Expect(resp.Code).To(Equal(http.StatusUnauthorized))
				Expect(sender.SendCommandCallCount()).To(Equal(0))
				Expect(metrics.IncWebhookSignatureRejectedCallCount()).To(Equal(1))
			},
		)

		It(
			"rejects an invalid signature with 401, no publish, increments rejection counter",
			func() {
				payload := workflowRunPayload("bborbe/repo")
				req := httptest.NewRequest(
					"POST",
					"/webhook/github-build",
					strings.NewReader(payload),
				)
				req.Header.Set("X-GitHub-Event", "workflow_run")
				req.Header.Set(
					"X-Hub-Signature-256",
					"sha256=0000000000000000000000000000000000000000000000000000000000000000",
				)
				resp := httptest.NewRecorder()
				h.ServeHTTP(resp, req)

				Expect(resp.Code).To(Equal(http.StatusUnauthorized))
				Expect(sender.SendCommandCallCount()).To(Equal(0))
				Expect(metrics.IncWebhookSignatureRejectedCallCount()).To(Equal(1))
			},
		)

		It("rejects everything when the secret is not configured (fail closed)", func() {
			closed := libhttp.NewErrorHandler(
				handler.NewWebhookHandler(
					sender,
					"",
					metrics,
					libtime.NewCurrentDateTime(),
					pkg.NewDebouncer(time.Second, libtime.NewCurrentDateTime()),
				),
			)
			req := webhookRequest("workflow_run", workflowRunPayload("bborbe/repo"))
			resp := httptest.NewRecorder()
			closed.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusUnauthorized))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})
	})

	Context("event routing", func() {
		It("acks ping with 200 and no publish", func() {
			req := webhookRequest("ping", `{"zen":"keep it simple"}`)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})

		It("acks an unsupported event with 200 and no publish", func() {
			req := webhookRequest("push", `{}`)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})
	})

	Context("workflow_run dispatch", func() {
		It("returns 202 and publishes the repo scope on completed failure", func() {
			req := webhookRequest("workflow_run", workflowRunPayload("bborbe/repo"))
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusAccepted))
			Expect(sender.SendCommandCallCount()).To(Equal(1))
			_, sentCmd := sender.SendCommandArgsForCall(0)
			Expect(sentCmd.Scope).To(Equal("bborbe/repo"))
			Expect(sentCmd.Force).To(BeFalse())
			Expect(metrics.IncWebhookDeliveryCallCount()).To(Equal(1))
			Expect(metrics.IncWebhookDeliveryArgsForCall(0)).To(Equal("success"))
			Expect(metrics.ObserveWebhookDispatchLatencyCallCount()).To(Equal(1))
		})

		It("rejects a malformed payload with 400", func() {
			req := webhookRequest("workflow_run", `{not json`)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusBadRequest))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})

		It("rejects a payload without repository.full_name with 400", func() {
			req := webhookRequest(
				"workflow_run",
				`{"action":"completed","workflow_run":{"conclusion":"failure","head_branch":"master"},"repository":{}}`,
			)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusBadRequest))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
		})

		It("returns 502 when the Kafka send fails", func() {
			sender.SendCommandReturns(errors.Errorf(ctx, "kafka error"))
			req := webhookRequest("workflow_run", workflowRunPayload("bborbe/repo"))
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusBadGateway))
			Expect(sender.SendCommandCallCount()).To(Equal(1))
		})

		It("acks but skips a non-completed action (200, no publish)", func() {
			payload := `{"action":"in_progress","workflow_run":{"conclusion":"","head_branch":"master"},"repository":{"full_name":"bborbe/repo","default_branch":"master"}}`
			req := webhookRequest("workflow_run", payload)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
			Expect(metrics.IncWebhookSkippedCallCount()).To(Equal(1))
			Expect(metrics.IncWebhookSkippedArgsForCall(0)).To(Equal("not_completed"))
		})

		It("acks but skips a non-failure conclusion (200, no publish)", func() {
			payload := `{"action":"completed","workflow_run":{"conclusion":"success","head_branch":"master"},"repository":{"full_name":"bborbe/repo","default_branch":"master"}}`
			req := webhookRequest("workflow_run", payload)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
			Expect(metrics.IncWebhookSkippedCallCount()).To(Equal(1))
			Expect(metrics.IncWebhookSkippedArgsForCall(0)).To(Equal("not_failure"))
		})

		It("acks but skips a non-default-branch run (200, no publish)", func() {
			payload := workflowRunPayloadOnBranch("bborbe/repo", "feature/x", "master")
			req := webhookRequest("workflow_run", payload)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusOK))
			Expect(sender.SendCommandCallCount()).To(Equal(0))
			Expect(metrics.IncWebhookSkippedCallCount()).To(Equal(1))
			Expect(metrics.IncWebhookSkippedArgsForCall(0)).To(Equal("not_default_branch"))
		})

		It("dispatches a failure on a non-master default branch", func() {
			payload := workflowRunPayloadOnBranch("bborbe/repo", "main", "main")
			req := webhookRequest("workflow_run", payload)
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)

			Expect(resp.Code).To(Equal(http.StatusAccepted))
			Expect(sender.SendCommandCallCount()).To(Equal(1))
			_, sentCmd := sender.SendCommandArgsForCall(0)
			Expect(sentCmd.Scope).To(Equal("bborbe/repo"))
		})

		It("debounces a second completed-failure delivery within the min interval", func() {
			h.ServeHTTP(
				&httptest.ResponseRecorder{},
				webhookRequest("workflow_run", workflowRunPayload("bborbe/repo")),
			)
			Expect(sender.SendCommandCallCount()).To(Equal(1))

			h.ServeHTTP(
				&httptest.ResponseRecorder{},
				webhookRequest("workflow_run", workflowRunPayload("bborbe/repo")),
			)

			Expect(sender.SendCommandCallCount()).To(Equal(1))
			Expect(metrics.IncWebhookSkippedCallCount()).To(Equal(1))
			Expect(metrics.IncWebhookSkippedArgsForCall(0)).To(Equal("debounced"))
		})
	})
})
