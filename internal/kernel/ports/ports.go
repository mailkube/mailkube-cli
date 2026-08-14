// Package ports declares the narrow SDK interfaces the CLI's features depend on.
//
// The interfaces are defined here, at the consumer, rather than exported by the SDK: each one
// names only the methods one feature actually calls, so a feature that sends mail cannot reach a
// method that cancels it, and a fake in a test implements two lines rather than a service.
//
// Every signature is copied from the published SDK, so its concrete services satisfy these as
// they stand. There is no adapter to write and nothing to keep in step; the only implementations
// this repository authors are the fakes.
package ports

import (
	"context"

	mailkube "github.com/mailkube/mailkube-go"
)

// EmailSender sends one message over the REST transport.
type EmailSender interface {
	// Send submits a message and returns what the server recorded about it.
	Send(ctx context.Context, params mailkube.SendEmailParams) (*mailkube.Email, error)
}
