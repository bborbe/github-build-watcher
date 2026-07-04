// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"net/http"
	"net/http/httptest"

	"github.com/bborbe/github-build-watcher/mocks"
	"github.com/bborbe/github-build-watcher/pkg/factory"
	libhttp "github.com/bborbe/http"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// CreateTriggerBuildCheckHandler is a one-line constructor delegate, but
// the test parity with github-pr's CreateSinglePRTriggerHandler and
// github-release's CreateTriggerReleaseCheckHandler factory smoke tests is
// load-bearing — it asserts the factory composes correctly + the resulting
// handler actually responds to a request without crashing.
var _ = Describe("CreateTriggerBuildCheckHandler", func() {
	var sender *mocks.TriggerBuildCheckCommandSender

	BeforeEach(func() {
		sender = new(mocks.TriggerBuildCheckCommandSender)
	})

	It("returns a non-nil handler", func() {
		h := factory.CreateTriggerBuildCheckHandler(sender)
		Expect(h).NotTo(BeNil())
	})

	It("handler responds to a request and publishes one command", func() {
		h := factory.CreateTriggerBuildCheckHandler(sender)
		wrapped := libhttp.NewErrorHandler(h)
		sender.SendCommandReturns(nil)
		req := httptest.NewRequest("POST", "/trigger", nil)
		//nolint:contextcheck // test setup uses Background; safe in tests
		req = req.WithContext(context.Background())
		resp := httptest.NewRecorder()
		wrapped.ServeHTTP(resp, req)
		Expect(resp.Code).To(Equal(http.StatusAccepted))
		Expect(sender.SendCommandCallCount()).To(Equal(1))
	})
})
