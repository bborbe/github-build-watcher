// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package command

import (
	"context"

	"github.com/bborbe/cqrs/base"
	cdb "github.com/bborbe/cqrs/cdb"
	cqrsiam "github.com/bborbe/cqrs/iam"
	"github.com/bborbe/errors"
	"github.com/golang/glog"

	lib "github.com/bborbe/maintainer"
)

//counterfeiter:generate -o ../../mocks/trigger_build_check_command_sender.go --fake-name TriggerBuildCheckCommandSender . TriggerBuildCheckCommandSender

// TriggerBuildCheckCommandSender sends TriggerBuildCheckCommand payloads to
// Kafka. Calls Validate before publishing — a validation error is
// returned without touching Kafka.
type TriggerBuildCheckCommandSender interface {
	SendCommand(ctx context.Context, cmd TriggerBuildCheckCommand) error
}

// NewTriggerBuildCheckCommandSender creates a TriggerBuildCheckCommandSender.
// The commandCreator and initiator are injected at construction time per
// the cqrs/docs/producing-commands.md "Factory Wiring" pattern (matches
// trading/frontend/command's reference impl) — built once at wiring, reused
// across every SendCommand call. The commandObjectSender wraps the Kafka
// sync producer.
func NewTriggerBuildCheckCommandSender(
	commandCreator base.CommandCreator,
	initiator cqrsiam.Initiator,
	commandObjectSender cdb.CommandObjectSender,
) TriggerBuildCheckCommandSender {
	return &triggerBuildCheckCommandSender{
		commandCreator:      commandCreator,
		initiator:           initiator,
		commandObjectSender: commandObjectSender,
	}
}

type triggerBuildCheckCommandSender struct {
	commandCreator      base.CommandCreator
	initiator           cqrsiam.Initiator
	commandObjectSender cdb.CommandObjectSender
}

func (s *triggerBuildCheckCommandSender) SendCommand(
	ctx context.Context,
	cmd TriggerBuildCheckCommand,
) error {
	if err := cmd.Validate(ctx); err != nil {
		return errors.Wrap(ctx, err, "validate TriggerBuildCheckCommand")
	}
	event, err := base.ParseEvent(ctx, cmd)
	if err != nil {
		return errors.Wrap(ctx, err, "parse TriggerBuildCheckCommand event")
	}
	commandObject := cdb.CommandObject{
		Command: s.commandCreator.NewCommand(
			TriggerBuildCheckCommandOperation,
			s.initiator,
			"",
			event,
		),
		SchemaID: lib.GithubBuildV1SchemaID,
	}
	if err := s.commandObjectSender.SendCommandObject(ctx, commandObject); err != nil {
		return errors.Wrap(ctx, err, "send TriggerBuildCheckCommand to Kafka")
	}
	glog.V(2).
		Infof("trigger sender: published op=%s scope=%q force=%t", TriggerBuildCheckCommandOperation, cmd.Scope, cmd.Force)
	return nil
}
