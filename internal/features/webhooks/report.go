package webhooks

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
)

// The stream's two destinations, and the rule dividing them.
//
// stdout carries one record per newly accepted, filter-passing event, and nothing else: that is
// the payload, it is what --exit-after counts, and it is what a pipe consumes. Everything that
// describes the run rather than the mail — the banner, a handshake, a recognised duplicate, a
// refusal, the closing summary — goes to stderr, where -q can silence it without ever touching
// the payload.

// banner announces what this listener is, before the first event arrives.
//
// It is long for a reason. Each line answers a question that otherwise becomes a support
// conversation: what to register, whether verification is on, which clock the timestamps use, and
// the two platform behaviours that surprise people.
func (s *session) banner() {
	deps, glyphs := s.deps, s.deps.Caps.Glyphs

	deps.Progress("")
	deps.Progress("  mailkube webhooks listen")
	deps.Progress("")
	deps.Progress("  Listening   %s", s.localURL())
	deps.Progress("  Public URL  %s   register this in the dashboard", output.Sanitize(s.cfg.publicURL))
	deps.Progress("  Signature   %s", s.signature())
	deps.Progress("  Times       UTC")
	if len(s.cfg.filter) > 0 {
		deps.Progress("  Filter      %s", s.filtered())
	}

	deps.Progress("")
	deps.Progress("  %s  Event routing is exclusive. Subscribing this URL to an event type on a", glyphs.Warn)
	deps.Progress("     production domain MOVES that event off your production endpoint, and")
	deps.Progress("     there is no redelivery. Use a test domain.")
	s.warnExposed()

	deps.Progress("")
	deps.Progress("  Next: add the public URL at %s", routes.Dashboard("/domain/webhooks"))
	deps.Progress("        Keep this running: registration probes it with a challenge request.")
	deps.Progress("")
	deps.Progress("  Waiting for events%s (Ctrl+C to stop)", glyphs.Ellipsis)
}

// localURL is where deliveries land on this machine.
func (s *session) localURL() string {
	if s.cfg.path == defaultPath {
		return "http://" + s.cfg.address
	}
	return "http://" + s.cfg.address + s.cfg.path
}

// signature says whether deliveries are being checked, and how strictly.
func (s *session) signature() string {
	if s.cfg.secret == "" {
		return "NOT CHECKED (--skip-verify): anyone who finds this URL can post to it"
	}
	return "verifying (tolerance " + s.o.tolerance.String() + ")"
}

// filtered lists the event types being handled, in a stable order.
func (s *session) filtered() string {
	types := make([]string, 0, len(s.cfg.filter))
	for eventType := range s.cfg.filter {
		types = append(types, output.Sanitize(eventType))
	}
	// Sorted rather than in the order the flag was given, so the line is the same on every run
	// and a golden file records a decision rather than a map iteration.
	sort.Strings(types)
	return strings.Join(types, ", ")
}

// warnExposed says so when the listener is reachable from beyond this machine.
//
// Not a refusal, because there is one case where it is correct: inside a container a published
// port cannot reach loopback, so the image would otherwise be unable to run the command it ships.
func (s *session) warnExposed() {
	if loopback(s.o.host) {
		return
	}
	s.deps.Progress("")
	s.deps.Progress("  %s  Bound to %s, so this listener is reachable from your network, not",
		s.deps.Caps.Glyphs.Warn, s.o.host)
	s.deps.Progress("     only from this machine. That is what a container needs and is not what")
	s.deps.Progress("     you want on a shared one.")
}

// loopback reports whether an address reaches only this machine.
func loopback(host string) bool {
	return host == defaultHost || host == "localhost"
}

// note writes one line about the run to the error stream.
//
// It lays the line out in the same columns as an event, so a duplicate or a refusal reads as part
// of the same stream rather than as an interruption of it.
//
// The caller holds the mutex, which is what stops two deliveries from interleaving mid-line.
func (s *session) note(badge string, at time.Time, subject, id, detail string) {
	line := fmt.Sprintf("  %s  %s  %s", badge, at.UTC().Format("15:04:05"), pad(subject, typeWidth))
	if id != "" {
		line += " " + pad(id, idWidth)
	}
	s.deps.Progress("%s  %s", line, detail)
}

// rejection describes a delivery that was not trusted, in terms the user can act on.
//
// A stale timestamp gets its own wording because it has its own cause and its own fix: the
// signature was computed correctly and the two machines disagree about the time, which is a
// different problem from a wrong secret and would otherwise be reported as the same thing.
func (s *session) rejection(r *http.Request, err error, now time.Time) (headline, hint string) {
	if !errors.Is(err, mailkube.ErrSignatureVerification) {
		return "rejected", output.Sanitize(err.Error())
	}

	if age, ok := staleness(r.Header.Get(headerDeliveryTimestamp), now, s.o.tolerance); ok {
		return "signature invalid", fmt.Sprintf("timestamp %s old; check your clock, `mailkube doctor` measures the skew",
			elapsed(age))
	}
	return "signature invalid", output.Sanitize(err.Error())
}

// staleness reports how far outside the tolerance a delivery timestamp is, if it is.
func staleness(timestamp string, now time.Time, tolerance time.Duration) (time.Duration, bool) {
	sent, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return 0, false
	}

	age := now.Sub(sent)
	if age < 0 {
		age = -age
	}
	if age <= tolerance {
		return 0, false
	}
	return age, true
}

// summary closes the run with what it saw.
func (s *session) summary() {
	s.mu.Lock()
	defer s.mu.Unlock()

	parts := []string{count(s.counts.events, "event", "events")}
	parts = appendCount(parts, s.counts.duplicates, "duplicate", "duplicates")
	parts = appendCount(parts, s.counts.filtered, "filtered", "filtered")
	parts = appendCount(parts, s.counts.rejected, "rejected", "rejected")
	parts = appendCount(parts, s.counts.handshakes, "handshake", "handshakes")
	if uptime := s.deps.Clock.Now().Sub(s.started); uptime >= time.Second {
		parts = append(parts, "uptime "+elapsed(uptime))
	}

	s.deps.Progress("")
	s.deps.Progress("  Summary  %s", strings.Join(parts, " "+s.deps.Caps.Glyphs.Bullet+" "))
}

// appendCount adds a tally, or leaves it out when there is nothing to say.
//
// A summary reading "0 duplicates · 0 rejected" makes the reader check two numbers that were
// never in doubt; the interesting ones are the ones that happened.
func appendCount(parts []string, n int, singular, plural string) []string {
	if n == 0 {
		return parts
	}
	return append(parts, count(n, singular, plural))
}

// count renders a tally with its word in the right number.
//
// Both forms are passed rather than an "s" appended, because half of these words are not nouns:
// "2 rejecteds" is what appending produces, and it is the kind of thing a reader notices instead
// of the number beside it.
func count(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}
