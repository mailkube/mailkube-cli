package webhooks_test

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/features/webhooks"
	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
)

// rendering is one event type and what its line has to say.
type rendering struct {
	// data is the event's data block.
	data string
	// says is what must appear on the rendered line, beyond the type itself.
	says []string
}

// renderings is one case per event type the SDK models.
//
// Keyed by the SDK's own type constants and checked against EventTypes() below, so this table
// cannot quietly fall behind: an event type added to the SDK fails this test until someone
// decides what its line should say, rather than rendering as a bare type with an empty column.
func renderings() map[string]rendering {
	const message = `"email_id":"9f3b2c14-7d8e-4a11-9c2f-8e5b1d0a3c77","to":["alice@example.com"],"subject":"Welcome"`

	return map[string]rendering{
		mailkube.EventTypeEmailSent: {
			data: `{` + message + `,"sent":{"recipient":"alice@example.com","timestamp":"2026-08-15T14:00:00Z"}}`,
			says: []string{"9f3b2c14", "alice@example.com"},
		},
		mailkube.EventTypeEmailDelivered: {
			data: `{` + message + `,"delivery":{"recipient":"alice@example.com","timestamp":"2026-08-15T14:00:00Z"}}`,
			says: []string{"9f3b2c14", "alice@example.com"},
		},
		mailkube.EventTypeEmailBounced: {
			data: `{` + message + `,"bounce":{"recipient":"bob@example.com","timestamp":"2026-08-15T14:00:00Z",` +
				`"code":550,"reason":"Mailbox does not exist"}}`,
			says: []string{"bob@example.com", "code 550", "Mailbox does not exist"},
		},
		mailkube.EventTypeEmailDeliveryDelayed: {
			data: `{` + message + `,"delay":{"recipient":"carol@example.com","timestamp":"2026-08-15T14:00:00Z",` +
				`"code":451,"reason":"Greylisted"}}`,
			says: []string{"carol@example.com", "code 451", "Greylisted"},
		},
		mailkube.EventTypeEmailSuppressed: {
			data: `{` + message + `,"suppression":{"recipients":["dana@example.com","erin@example.com"],` +
				`"timestamp":"2026-08-15T14:00:00Z"}}`,
			says: []string{"dana@example.com", "erin@example.com"},
		},
		mailkube.EventTypeEmailScheduled: {
			data: `{` + message + `,"scheduled":{"scheduled_at":"2026-08-16T09:00:00Z","batch_id":null}}`,
			says: []string{"9f3b2c14", "alice@example.com"},
		},
		mailkube.EventTypeEmailFailed: {
			data: `{` + message + `,"failed":{"reason":"body_scan_rejected","timestamp":"2026-08-15T14:00:00Z"}}`,
			says: []string{"alice@example.com", "body_scan_rejected"},
		},
		mailkube.EventTypeEmailOpened: {
			data: `{` + message + `,"open":{"ipAddress":"203.0.113.4","userAgent":"Mail/1.0",` +
				`"timestamp":"2026-08-15T14:00:00Z"}}`,
			says: []string{"alice@example.com"},
		},
		mailkube.EventTypeEmailClicked: {
			data: `{` + message + `,"click":{"ipAddress":"203.0.113.4","userAgent":"Mail/1.0",` +
				`"timestamp":"2026-08-15T14:00:00Z","link":"https://acme.example/offer"}}`,
			says: []string{"alice@example.com", "https://acme.example/offer"},
		},
		mailkube.EventTypeDomainStatus: {
			// No message context at all: this event is about a domain, so the column that
			// usually carries a recipient carries the domain instead.
			data: `{"domain":"acme.example","status":"verified","onboarding_state":"complete",` +
				`"previous":{"status":"pending","onboarding_state":"dns"}}`,
			says: []string{"acme.example", "verified"},
		},
		mailkube.EventTypeWebhookStatus: {
			data: `{"endpoint_url":"https://a1b2.example.com","is_active":false,"is_deleted":false,` +
				`"disabled_reason":"too_many_failures","previous":{"is_active":true,"is_deleted":false,` +
				`"disabled_reason":""}}`,
			says: []string{"a1b2.example.com", "too_many_failures"},
		},
	}
}

func TestEveryModelledEventTypeRenders(t *testing.T) {
	t.Parallel()

	table := renderings()

	// The anchor: the SDK's catalogue is the list of event types that exist, and this table
	// has to cover it. Without this the table could drift into describing an older platform
	// while every case in it still passed.
	var missing []string
	for _, eventType := range mailkube.EventTypes() {
		if _, covered := table[eventType]; !covered {
			missing = append(missing, eventType)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("the SDK models event types this test does not render: %s", strings.Join(missing, ", "))
	}

	types := mailkube.EventTypes()
	run := start(t, running(), "--public-url", "https://a1b2.example.com",
		"--exit-after", strconv.Itoa(len(types)), "--exit-timeout", "20s")

	for i, eventType := range types {
		body := `{"type":"` + eventType + `","created_at":"2026-08-15T14:00:00Z","data":` + table[eventType].data + `}`
		run.deliver(t, "d"+strconv.Itoa(i), body)
	}
	run.wait(t)

	if run.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", run.code, run.errOut)
	}

	shown := run.out.String()
	for _, eventType := range types {
		if !strings.Contains(shown, eventType) {
			t.Errorf("%s did not render:\n%s", eventType, shown)
		}
		for _, want := range table[eventType].says {
			if !strings.Contains(shown, want) {
				t.Errorf("the %s line does not carry %q:\n%s", eventType, want, shown)
			}
		}
	}
}

func TestAnEventTypeThisReleaseDoesNotModelStillArrives(t *testing.T) {
	t.Parallel()

	// A receiver built against an older SDK must keep working when the platform introduces an
	// event. It has a type, an id and a body, which is enough to see that it arrived and
	// enough for a pipe to carry it on.
	run := start(t, running(), "--public-url", "https://a1b2.example.com", "--exit-after", "1")
	run.deliver(t, "d1", `{"type":"email.teleported","created_at":"2026-08-15T14:00:00Z",`+
		`"data":{"email_id":"9f3b2c14","destination":"mars"}}`)
	run.wait(t)

	if run.code != errs.CodeOK {
		t.Fatalf("exit code = %d: %s", run.code, run.errOut)
	}
	if !strings.Contains(run.out.String(), "email.teleported") {
		t.Errorf("an unmodelled event did not render:\n%s", run.out)
	}
}

func TestTheFeatureDescribesItselfToTheRegistry(t *testing.T) {
	t.Parallel()

	// The registry is the only place the command set is written down, and the root screen is
	// built from what each feature says about itself. A feature that named itself wrong would
	// still work and would be listed under the wrong heading.
	f := webhooks.New()
	if f.Name() != "webhooks" {
		t.Errorf("Name() = %q", f.Name())
	}

	entries := f.HelpEntries()
	if len(entries) != 1 {
		t.Fatalf("HelpEntries() returned %d entries, want 1", len(entries))
	}
	if entries[0].Group != feature.GroupDevelop {
		t.Errorf("the listener is grouped under %q, not the development loop", entries[0].Group)
	}
	if entries[0].Invocation != "webhooks listen" {
		t.Errorf("Invocation = %q", entries[0].Invocation)
	}
}
