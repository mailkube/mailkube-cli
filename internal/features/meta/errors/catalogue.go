package errors

import mailkube "github.com/mailkube/mailkube-go"

// Entry is one explained failure.
//
// The fields are what a person needs in the order they need them: what happened, whether doing
// the same thing again could work, and what to do instead. An entry that only restates the name
// is worse than none, because it teaches the reader that this command has nothing to offer.
type Entry struct {
	// Name is the machine-readable error name, or the SMTP code with its enhanced status.
	Name string `json:"name"`
	// Status is the HTTP status the API answers with, or the SMTP reply code.
	Status int `json:"status,omitempty"`
	// Enhanced is the SMTP enhanced status code, on an SMTP entry.
	Enhanced string `json:"enhanced,omitempty"`
	// Retryable says whether re-running the identical request could succeed.
	Retryable bool `json:"retryable"`
	// Summary is one sentence saying what happened.
	Summary string `json:"summary"`
	// Actions are the checks and fixes, in the order worth trying them.
	Actions []Action `json:"actions,omitempty"`
	// Note is anything surprising that is true and easy to be caught out by.
	Note string `json:"note,omitempty"`
	// SMTP marks the entry as belonging to the submission catalogue rather than the API one.
	SMTP bool `json:"smtp,omitempty"`
}

// Action is one thing to check or one thing to do.
type Action struct {
	// Kind is "Check" or "Fix".
	Kind string `json:"kind"`
	// Detail is the instruction.
	Detail string `json:"detail"`
}

// check and fix build the two kinds of action.
func check(detail string) Action { return Action{Kind: "Check", Detail: detail} }
func fix(detail string) Action   { return Action{Kind: "Fix", Detail: detail} }

// apiCatalogue explains every documented API error name.
//
// Every name the SDK declares has an entry, and a test parses the SDK's own source to prove it,
// so a name added upstream fails the build here rather than reaching a user unexplained. The set
// is not closed: an unknown name still renders, because the platform may know names this release
// does not.
//
//nolint:funlen // a data table; splitting it would hide the one thing worth seeing at a glance
func apiCatalogue() []Entry {
	return []Entry{
		{
			Name: mailkube.ErrorNameApplicationError, Status: 500, Retryable: true,
			Summary: "The platform failed to handle the request.",
			Actions: []Action{fix("Re-run the command. If it persists, quote the request id to support.")},
		},
		{
			Name: mailkube.ErrorNameBodyContentRejected, Status: 422,
			Summary: "The message body was rejected by the content scan.",
			Actions: []Action{
				check("Links in the body resolve to hosts you control and are not newly registered."),
				check("The body is not a forwarded copy of something already flagged."),
			},
		},
		{
			Name: mailkube.ErrorNameBrowserNotAllowed, Status: 403,
			Summary: "The request came from a browser, which cannot hold an API key safely.",
			Actions: []Action{fix("Call the API from a server you control, never from page JavaScript.")},
			Note:    "This is a client-configuration precondition, not a credential problem.",
		},
		{
			Name: mailkube.ErrorNameConcurrentIdempotentRequest, Status: 409, Retryable: true,
			Summary: "Another request with the same idempotency key is still in flight.",
			Actions: []Action{fix("Wait for the first request to finish, then re-run.")},
		},
		{
			Name: mailkube.ErrorNameFromDomainNotAllowed, Status: 422,
			Summary: "The from address is not on a domain this API key can send from.",
			Actions: []Action{
				check("The message names the domain the key is bound to; use an address on it."),
				check("`mailkube auth status` shows which key is in use."),
			},
		},
		{
			Name: mailkube.ErrorNameInvalidAPIKey, Status: 403,
			Summary: "The API key was not accepted.",
			Actions: []Action{check("`mailkube auth status` shows which key is in use and where it came from.")},
			Note:    "A malformed key and an unknown key are deliberately indistinguishable.",
		},
		{
			Name: mailkube.ErrorNameInvalidAttachment, Status: 422,
			Summary: "An attachment could not be read.",
			Actions: []Action{
				check("The content is base64 and the filename carries an extension."),
				check("`--dry-run` shows each attachment's name and type without printing its content."),
			},
		},
		{
			Name: mailkube.ErrorNameInvalidFromAddress, Status: 422,
			Summary: "The from address is not a valid address.",
			Actions: []Action{fix(`Write it as hello@acme.com, or as "Acme <hello@acme.com>".`)},
		},
		{
			Name: mailkube.ErrorNameInvalidIdempotencyKey, Status: 422,
			Summary: "The idempotency key is not in an acceptable form.",
			Actions: []Action{fix("Use --idempotency-key auto to derive one from the message.")},
		},
		{
			Name: mailkube.ErrorNameInvalidIdempotentRequest, Status: 409,
			Summary: "This idempotency key was already used for a different message.",
			Actions: []Action{
				fix("Use a new key, or send the identical message the key was first bound to."),
			},
			Note: "A key is bound to the body it first accompanied, which is what makes a replay safe.",
		},
		{
			Name: mailkube.ErrorNameInvalidRequestBody, Status: 400,
			Summary: "The request body was not readable as JSON.",
			Actions: []Action{check("`mailkube emails send --generate-skeleton` emits the expected shape.")},
		},
		{
			Name: mailkube.ErrorNameLinkReputationBlocked, Status: 422,
			Summary: "A link in the message is on a host with poor reputation.",
			Actions: []Action{
				check("Every link resolves to a host you control."),
				check("Shortener links are a common cause; use the destination URL directly."),
			},
		},
		{
			Name: mailkube.ErrorNameMaxMessageSizeExceeded, Status: 422,
			Summary: "The message is larger than your plan allows.",
			Actions: []Action{
				check("Attachments are base64 on the wire, so they cost about a third more than the file."),
				fix("Link to the file instead of attaching it."),
			},
		},
		{
			Name: mailkube.ErrorNameMaxRecipientsExceeded, Status: 422,
			Summary: "The message names more recipients than your plan allows.",
			Actions: []Action{fix("Split the send, or upgrade the plan.")},
		},
		{
			Name: mailkube.ErrorNameMethodNotAllowed, Status: 405,
			Summary: "The endpoint does not accept that HTTP method.",
			Actions: []Action{check("Whether the CLI and the SDK are up to date.")},
		},
		{
			Name: mailkube.ErrorNameMissingRequiredField, Status: 422,
			Summary: "A required field was absent.",
			Actions: []Action{check("The message names the field. The skeleton marks every required one.")},
		},
		{
			Name: mailkube.ErrorNameMissingRequiredVariable, Status: 422,
			Summary: "The template needs a variable the request did not supply.",
			Actions: []Action{fix("Pass it with --var name=value, or supply the set with --vars @file.")},
		},
		{
			Name: mailkube.ErrorNameMissingUserAgent, Status: 400,
			Summary: "The request carried no User-Agent.",
			Actions: []Action{check("Whether something between you and the platform is stripping headers.")},
		},
		{
			Name: mailkube.ErrorNameNotAcceptable, Status: 406,
			Summary: "The request asked for a representation the endpoint cannot produce.",
			Actions: []Action{check("Whether the CLI and the SDK are up to date.")},
		},
		{
			Name: mailkube.ErrorNameQuotaExceeded, Status: 422,
			Summary: "Your organisation's message quota for the current period is exhausted.",
			Actions: []Action{fix("Upgrade the plan, or wait for the period to reset.")},
			Note: "Suppressed recipients still consume quota: a suppressed address is accepted, " +
				"charged and dropped, visible only as an email.suppressed webhook event.",
		},
		{
			Name: mailkube.ErrorNameRateLimitExceeded, Status: 429, Retryable: true,
			Summary: "Requests are arriving faster than your plan allows.",
			Actions: []Action{
				check("The Retry-After value is reported; nothing here retries on its own."),
				fix("Use --idempotency-key so a retry cannot become a second charged message."),
			},
		},
		{
			Name: mailkube.ErrorNameScheduledEmailNotFound, Status: 404,
			Summary: "No scheduled email has that id.",
			Actions: []Action{
				check("The id is the full one from the send acknowledgement, not an abbreviated one."),
				check("A send that has already gone out has left the collection."),
			},
		},
		{
			Name: mailkube.ErrorNameScheduledEmailNotPending, Status: 422,
			Summary: "That scheduled email is no longer pending, so it cannot be changed.",
			Actions: []Action{check("`mailkube scheduled-emails get <id>` shows its current status.")},
		},
		{
			Name: mailkube.ErrorNameSchedulingNotIncluded, Status: 403,
			Summary: "Scheduled sending is not included in your plan.",
			Actions: []Action{fix("Send without --at, or upgrade the plan.")},
			Note:    "This is a plan entitlement, not a credential problem: re-checking your key will not help.",
		},
		{
			Name: mailkube.ErrorNameTemplateNotFound, Status: 404,
			Summary: "No template has that id.",
			Actions: []Action{check("Templates are managed in the dashboard; the id is shown there.")},
		},
		{
			Name: mailkube.ErrorNameTemplateNotPublished, Status: 422,
			Summary: "The template exists but has no published version to render.",
			Actions: []Action{fix("Publish it in the dashboard, or name a version with --template-version.")},
		},
		{
			Name: mailkube.ErrorNameTopicDisabled, Status: 422,
			Summary: "That topic exists but is switched off.",
			Actions: []Action{fix("Enable it in the dashboard, or send without --topic.")},
		},
		{
			Name: mailkube.ErrorNameTopicNotFound, Status: 422,
			Summary: "No topic with that slug exists on the sending domain.",
			Actions: []Action{check("Topics belong to a domain; the slug must exist on the one you send from.")},
		},
		{
			Name: mailkube.ErrorNameUnsupportedMediaType, Status: 415,
			Summary: "The request's content type is not one the endpoint accepts.",
			Actions: []Action{check("Whether the CLI and the SDK are up to date.")},
		},
		{
			Name: mailkube.ErrorNameValidationError, Status: 422,
			Summary: "The request was well-formed but a field failed validation.",
			Actions: []Action{check("The message names the field and the rule it broke.")},
		},
	}
}

// smtpCatalogue explains the submission failures worth knowing by heart.
//
// It exists because the CLI renders its own explanation of an SMTP failure rather than the
// server's free text, so that explanation has to come from somewhere. Keyed on the reply code
// and, where it distinguishes two situations, the enhanced status alongside it.
func smtpCatalogue() []Entry {
	return []Entry{
		{
			Name: "421", Status: 421, SMTP: true, Retryable: true,
			Summary: "The service is not available and the connection is closing.",
			Actions: []Action{fix("Re-run the command. Nothing here retries on its own.")},
		},
		{
			Name: "450", Status: 450, SMTP: true, Retryable: true,
			Summary: "The mailbox was unavailable, temporarily.",
			Actions: []Action{fix("Re-run later; the address itself is not necessarily wrong.")},
		},
		{
			Name: "452", Status: 452, SMTP: true, Retryable: true,
			Summary: "The server has insufficient storage to accept the message now.",
			Actions: []Action{fix("Re-run later, or send a smaller message.")},
		},
		{
			Name: "454", Status: 454, Enhanced: "4.7.0", SMTP: true, Retryable: true,
			Summary: "Authentication was refused temporarily, usually because it is being throttled.",
			Actions: []Action{
				check("Sign-in is limited per sending domain; hold one session open rather than one per message."),
				fix("Wait a moment and re-run."),
			},
		},
		{
			Name: "535", Status: 535, Enhanced: "5.7.8", SMTP: true,
			Summary: "The username or password was rejected.",
			Actions: []Action{
				check("The username is localpart@verified-domain, not a bare name."),
				check("The password is the SMTP credential from the dashboard, not your API key."),
				fix(dashboardURL("/domain/credentials#smtp")),
			},
		},
		{
			Name: "550", Status: 550, SMTP: true,
			Summary: "The mailbox was unavailable, permanently.",
			Actions: []Action{
				check("The address exists and is spelled correctly."),
				check("The recipient has not been suppressed after an earlier bounce."),
			},
		},
		{
			Name: "552", Status: 552, SMTP: true,
			Summary: "The message exceeded a size limit.",
			Actions: []Action{
				check("`mailkube smtp test` reports the size the server advertises."),
				fix("Link to large files instead of attaching them."),
			},
		},
		{
			Name: "554", Status: 554, SMTP: true,
			Summary: "The transaction failed, and the server will not accept this message.",
			Actions: []Action{
				check("The from address is on a domain this credential may send from."),
				check("The content and its links, which are scanned before submission."),
			},
		},
	}
}

// catalogue is both halves, in the order `--list` shows them.
func catalogue() []Entry { return append(apiCatalogue(), smtpCatalogue()...) }

// APINames returns the names the API half explains, for the parity check.
func APINames() []string {
	entries := apiCatalogue()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}
