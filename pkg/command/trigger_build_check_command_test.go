// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command_test

import (
	"context"
	"encoding/json"

	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/github-build-watcher/pkg/command"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TriggerBuildCheckCommandOperation", func() {
	It("has expected string value", func() {
		Expect(command.TriggerBuildCheckCommandOperation).
			To(Equal(base.CommandOperation("trigger-build-check")))
	})

	It("passes cqrs operation regex validation", func() {
		// Boundary test: catches renames that violate the
		// `^[a-z][a-z-]*$` cqrs wire-string regex (e.g. underscores,
		// leading digit, uppercase). Per agent/lib precedent every
		// CommandOperation constant gets this check.
		Expect(command.TriggerBuildCheckCommandOperation.Validate(context.Background())).
			To(Succeed())
	})
})

var _ = Describe("TriggerBuildCheckCommand", func() {
	It("round-trips through JSON with both fields set", func() {
		cmd := command.TriggerBuildCheckCommand{
			Scope: "bborbe/repo",
			Force: true,
		}
		data, err := json.Marshal(cmd)
		Expect(err).To(BeNil())

		var got command.TriggerBuildCheckCommand
		Expect(json.Unmarshal(data, &got)).To(Succeed())
		Expect(got.Scope).To(Equal(cmd.Scope))
		Expect(got.Force).To(Equal(cmd.Force))
	})

	It("omits scope and force when zero (omitempty)", func() {
		cmd := command.TriggerBuildCheckCommand{}
		data, err := json.Marshal(cmd)
		Expect(err).To(BeNil())
		jsonStr := string(data)
		Expect(jsonStr).NotTo(ContainSubstring("\"scope\""))
		Expect(jsonStr).NotTo(ContainSubstring("\"force\""))
	})

	It("JSON contains scope and force keys when set", func() {
		cmd := command.TriggerBuildCheckCommand{
			Scope: "bborbe/repo",
			Force: true,
		}
		data, err := json.Marshal(cmd)
		Expect(err).To(BeNil())
		jsonStr := string(data)
		Expect(jsonStr).To(ContainSubstring(`"scope"`))
		Expect(jsonStr).To(ContainSubstring(`"force"`))
	})
})

var _ = Describe("TriggerBuildCheckCommand.Validate", func() {
	It("accepts the empty payload {}", func() {
		cmd := command.TriggerBuildCheckCommand{}
		Expect(cmd.Validate(context.Background())).To(Succeed())
	})

	It("accepts a populated payload (Scope and Force)", func() {
		cmd := command.TriggerBuildCheckCommand{
			Scope: "bborbe/repo",
			Force: true,
		}
		Expect(cmd.Validate(context.Background())).To(Succeed())
	})
})
