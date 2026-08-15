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
	"iter"

	mailkube "github.com/mailkube/mailkube-go"

	mksmtp "github.com/mailkube/mailkube-cli/internal/kernel/smtp"
)

// EmailSender sends one message over the REST transport.
type EmailSender interface {
	// Send submits a message and returns what the server recorded about it.
	Send(ctx context.Context, params mailkube.SendEmailParams) (*mailkube.Email, error)
}

// SMTPSubmitter submits one message over SMTP.
//
// The CLI depends on this rather than on the client itself, so the send path and the connectivity
// probe share one implementation and a test substitutes two methods rather than a session.
type SMTPSubmitter interface {
	// Send submits one message on an already-authenticated session.
	Send(message mksmtp.Message) error
	// Capabilities is what the server advertised and what was negotiated.
	Capabilities() mksmtp.Capabilities
	// Close ends the session.
	Close()
}

// ScheduleReader reads the scheduled-email collection.
//
// Reading and writing are separate interfaces because the commands are separate: `list` and
// `get` cannot cancel anything, and a fake for either is two methods rather than six.
type ScheduleReader interface {
	// List returns one page.
	List(ctx context.Context, params mailkube.ScheduledEmailListParams) (*mailkube.ScheduledEmailPage, error)
	// All walks every page, fetching lazily, so abandoning the loop early costs nothing.
	All(ctx context.Context, params mailkube.ScheduledEmailListParams) iter.Seq2[*mailkube.ScheduledEmail, error]
	// Get retrieves one scheduled email by id.
	Get(ctx context.Context, emailID string) (*mailkube.ScheduledEmail, error)
}

// ScheduleWriter reschedules and cancels one scheduled email at a time.
type ScheduleWriter interface {
	// Update reschedules one email, optionally moving it into a batch.
	Update(
		ctx context.Context, emailID string, params mailkube.ScheduledEmailUpdateParams,
	) (*mailkube.ScheduledEmail, error)
	// Cancel cancels one email.
	Cancel(ctx context.Context, emailID string) (*mailkube.CanceledScheduledEmail, error)
}

// BatchWriter reschedules and cancels a whole batch at once.
type BatchWriter interface {
	// Update reschedules every pending email in a batch.
	Update(
		ctx context.Context, batchID string, params mailkube.ScheduledEmailBatchUpdateParams,
	) (*mailkube.ScheduledEmailBatchUpdate, error)
	// Cancel cancels every pending email in a batch.
	Cancel(ctx context.Context, batchID string) (*mailkube.ScheduledEmailBatchCancel, error)
}
