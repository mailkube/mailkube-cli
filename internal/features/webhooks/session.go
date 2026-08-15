package webhooks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
)

const (
	// handshakeDeadline matches the time the platform allows a registration probe to answer.
	// A client that opens a connection and then sends nothing must not be able to hold one
	// open past the point where an answer would still have counted.
	handshakeDeadline = 5 * time.Second
	// drainGrace is how long in-flight deliveries have to finish once a stop is requested.
	drainGrace = 5 * time.Second
	// rememberedDeliveries bounds the deduplication memory.
	rememberedDeliveries = 4096
	// minFieldWidth is the narrowest a rendered field may be clamped to.
	minFieldWidth = 16
)

// The delivery headers this package reads for its own reporting.
//
// Verification is the SDK's, and it reads these itself; these constants exist only so a rejected
// delivery can be described in terms a user can act on, and so the unverified path can still
// label an event with the id it arrived under.
const (
	headerDeliveryID        = "X-Webhook-Id"
	headerDeliveryTimestamp = "X-Webhook-Ts"
)

// counts is what the closing summary reports.
type counts struct {
	// events is the number of new, matching deliveries handled.
	events int
	// duplicates is the number of redeliveries recognised and not handled twice.
	duplicates int
	// filtered is the number of deliveries --filter excluded.
	filtered int
	// rejected is the number of deliveries that failed verification or parsing.
	rejected int
	// handshakes is the number of registration probes answered.
	handshakes int
}

// session is one run of the listener.
type session struct {
	deps *feature.Deps
	o    *options
	cfg  config

	// started is when the run began, for the uptime in the summary.
	started time.Time

	// mu guards everything below it, and is held across a whole delivery. Rendering is part
	// of what it protects: deliveries arrive on their own goroutines, and two events writing
	// a line each at the same moment would interleave halfway through.
	mu sync.Mutex
	// counts is the running tally.
	counts counts
	// seen deduplicates on the delivery id.
	seen *recent
	// timeline remembers when a message was first heard of, so a later outcome for the same
	// message can say how long it took.
	timeline *recent

	// done is closed when the run should end of its own accord.
	done chan struct{}
	// failure is why, and is nil when the run simply got what it was waiting for.
	failure error
	// once makes the first reason to stop the only one.
	once sync.Once
}

// newSession prepares a run.
func newSession(deps *feature.Deps, o *options, cfg config) *session {
	return &session{
		deps:     deps,
		o:        o,
		cfg:      cfg,
		started:  deps.Clock.Now(),
		seen:     newRecent(rememberedDeliveries),
		timeline: newRecent(rememberedDeliveries),
		done:     make(chan struct{}),
	}
}

// serve runs the listener until something stops it, then reports what happened.
func (s *session) serve(ctx context.Context, socket net.Listener) error {
	server := &http.Server{Handler: s.handler(), ReadHeaderTimeout: handshakeDeadline}

	serving := make(chan error, 1)
	go func() { serving <- server.Serve(socket) }()

	err := s.wait(ctx, serving)
	s.shutdown(server)
	s.summary()
	return err
}

// wait blocks until the run is over and returns the outcome.
func (s *session) wait(ctx context.Context, serving <-chan error) error {
	// Satisfying --exit-after at the same moment a signal arrives is a race the caller should
	// win: the assertion they wrote succeeded, and reporting an interrupt would fail a CI step
	// that had in fact passed. Checking first rather than relying on select, which chooses
	// among ready cases at random, is what makes that a rule instead of a coin toss.
	select {
	case <-s.done:
		return s.failure
	default:
	}

	// The timer is the wall clock rather than the injected one on purpose: --exit-timeout is a
	// real deadline in the world, not a rendered value, and a fixed test clock that never
	// advanced would make it unreachable.
	var deadline <-chan time.Time
	if s.o.exitTimeout > 0 {
		timer := time.NewTimer(s.o.exitTimeout)
		defer timer.Stop()
		deadline = timer.C
	}

	select {
	case <-s.done:
		return s.failure
	case err := <-serving:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errs.WithCode(errs.CodeNetwork, err)
	case <-deadline:
		return errs.Newf(errs.CodeDeadline, "%s", s.deadlineReason())
	case <-ctx.Done():
		// Being stopped is how this command usually ends, so it says that rather than
		// surfacing Go's own "context canceled", which describes the mechanism and not the
		// event. The code stays 130, because a script watching this still has to tell a
		// stop apart from an assertion that was satisfied.
		return errs.Newf(errs.CodeInterrupt, "stopped")
	}
}

// deadlineReason says what did not happen in time.
func (s *session) deadlineReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.o.exitAfter > 0 {
		return fmt.Sprintf("%d of %d matching events arrived within %s",
			s.counts.events, s.o.exitAfter, s.o.exitTimeout)
	}
	return fmt.Sprintf("no event arrived within %s", s.o.exitTimeout)
}

// shutdown stops the server, giving in-flight deliveries a bounded moment to finish.
func (s *session) shutdown(server *http.Server) {
	// A fresh context, because by the time we are here the caller's is usually already
	// cancelled, and a drain that inherited that cancellation would not drain.
	ctx, cancel := context.WithTimeout(context.Background(), drainGrace)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		// The grace window expired with work still running. Ending it is the honest move:
		// the alternative is a command that will not exit when asked twice.
		_ = server.Close()
	}
}

// finish records the first reason to stop, and only the first.
func (s *session) finish(err error) {
	s.once.Do(func() {
		s.failure = err
		close(s.done)
	})
}

// handler builds the endpoint: the registration handshake and every delivery.
func (s *session) handler() http.Handler {
	sdk := mailkube.WebhookHandler{
		Secret:       s.cfg.secret,
		Tolerance:    s.o.tolerance,
		MaxBodyBytes: s.cfg.maxBody,
		OnEvent:      s.accept,
		OnError:      s.reject,
	}

	// A mux rather than the handler alone, so a delivery to the wrong path is a 404 rather
	// than silently accepted. Registering an endpoint at /webhooks and listening on / is a
	// mistake worth being told about.
	mux := http.NewServeMux()
	mux.Handle(s.cfg.path, s.route(sdk))
	return mux
}

// route sends each request to the code that should answer it.
func (s *session) route(sdk mailkube.WebhookHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			// The handshake is the SDK's, unchanged. It is the same code a customer's own
			// endpoint runs, so answering a registration probe here differently would mean
			// rehearsing against something the platform never talks to.
			answered := &answer{ResponseWriter: w, code: http.StatusOK}
			sdk.ServeHTTP(answered, r)
			s.handshook(r, answered.code)
		case s.cfg.secret == "":
			s.deliverUnverified(w, r)
		default:
			sdk.ServeHTTP(w, r)
		}
	}
}

// answer records the status a handler wrote, so the outcome can be reported.
type answer struct {
	http.ResponseWriter
	// code is the status written, defaulting to the one net/http assumes.
	code int
}

// WriteHeader implements http.ResponseWriter.
func (a *answer) WriteHeader(code int) {
	a.code = code
	a.ResponseWriter.WriteHeader(code)
}

// deliverUnverified handles a delivery when the user asked for no signature check.
//
// It exists rather than reusing the SDK's path because that path verifies, which is the whole of
// what has been waived here. Everything else is the same, including the body cap: an unverified
// endpoint is more exposed than a verified one, not less.
func (s *session) deliverUnverified(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.maxBody))
	if err != nil {
		s.reject(w, r, err)
		return
	}

	event, err := mailkube.ParseEvent(body)
	if err != nil {
		s.reject(w, r, err)
		return
	}
	event.ID = r.Header.Get(headerDeliveryID)
	event.Timestamp = r.Header.Get(headerDeliveryTimestamp)

	if err := s.accept(r.Context(), event); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// accept handles one delivery that got this far.
//
// The order is filter, then deduplicate, then count. Filtering first is what keeps the summary
// truthful: "1 duplicate" should mean a repeat of something this run actually handled, not a
// repeat of something it was never interested in.
func (s *session) accept(_ context.Context, event *mailkube.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	at := s.deps.Clock.Now()
	if len(s.cfg.filter) > 0 && !s.cfg.filter[event.Type] {
		s.counts.filtered++
		return nil
	}
	if s.repeated(event, at) {
		return nil
	}

	s.counts.events++
	if err := s.show(event, s.observe(event, at)); err != nil {
		// Nothing left to report with, and nothing worth continuing for: this command's
		// whole job is to write these events somewhere.
		s.finish(err)
		return err
	}
	if s.o.exitAfter > 0 && s.counts.events >= s.o.exitAfter {
		s.finish(nil)
	}
	return nil
}

// repeated reports whether this delivery has already been handled, and says so if it has.
func (s *session) repeated(event *mailkube.Event, at time.Time) bool {
	// An unverified delivery may carry no id at all, and treating every one of those as a
	// repeat of the first would silently drop the whole stream.
	if event.ID == "" {
		return false
	}
	if _, seen := s.seen.add(event.ID, at); !seen {
		return false
	}

	s.counts.duplicates++
	// The message id, not the delivery id, so this line sits under the event it repeats in the
	// same column showing the same value. The delivery id is what identified it as a repeat;
	// the message id is what the reader is following down the screen.
	s.note(s.deps.Caps.Glyphs.Dup, at, output.Sanitize(event.Type),
		shorten(output.Sanitize(detailOf(event.Data).emailID)), "duplicate, already handled")
	return true
}

// observe turns a delivery into the value both output formats render.
func (s *session) observe(event *mailkube.Event, at time.Time) EventView {
	found := detailOf(event.Data)

	view := EventView{
		ID:         event.ID,
		Type:       event.Type,
		EmailID:    found.emailID,
		Recipient:  found.who,
		ReceivedAt: at.UTC().Format(time.RFC3339),
		Verified:   s.cfg.secret != "",
		Payload:    event.Raw,
		at:         at,
		code:       found.code,
		reason:     found.reason,
		width:      s.o.maxField,
	}

	// How long a message took to reach this outcome is the question a stream is usually being
	// watched to answer, and it is one nothing in the payload can answer on its own: the
	// events carry their own timestamps, but only a listener that saw both can subtract them.
	if found.emailID != "" {
		if first, known := s.timeline.add(found.emailID, at); known {
			view.since = at.Sub(first)
		}
	}
	return view
}

// show writes one accepted event to the payload stream.
//
// The format wins over --print when one was asked for, because -o names what a program will
// parse and --print names how a person wants to read it; when both are stated, only one of them
// has a consumer that will break.
func (s *session) show(event *mailkube.Event, view EventView) error {
	if s.deps.Format != output.Text {
		return s.deps.Emit(view)
	}

	switch s.o.print {
	case printSummary:
		return nil
	case printRaw:
		return s.raw(event)
	default:
		return s.deps.Emit(view)
	}
}

// raw writes the delivered body rather than this program's reading of it.
//
// This is the invariant applied literally: raw bytes to files and pipes, sanitised text to
// terminals. JSON cannot carry a literal escape character inside a string, but it can carry a
// right-to-left override and an 8-bit control introducer, and both of those act on a terminal.
func (s *session) raw(event *mailkube.Event) error {
	body := string(event.Raw)
	if s.deps.Caps.TTY {
		body = output.Sanitize(body)
	}
	return output.WriteLines(s.deps.IO.Out, []string{body})
}

// reject reports a delivery that could not be trusted, and answers it.
//
// Taking over the error response is what buys the reason: the SDK writes a status and nothing
// else, and "something was refused" is not a report anyone can act on. The status mapping below
// restates the SDK's own, which is the cost of that trade and the reason it is written out rather
// than approximated.
func (s *session) reject(w http.ResponseWriter, r *http.Request, err error) {
	s.mu.Lock()
	at := s.deps.Clock.Now()
	s.counts.rejected++
	headline, hint := s.rejection(r, err, at)
	s.note(s.deps.Caps.Glyphs.Warn, at, headline, "", hint)
	s.mu.Unlock()

	status := statusFor(err)
	http.Error(w, http.StatusText(status), status)
}

// statusFor maps a refusal to the status the platform expects to see.
func statusFor(err error) int {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, mailkube.ErrSignatureVerification):
		return http.StatusUnauthorized
	default:
		return http.StatusBadRequest
	}
}

// handshook reports a registration probe.
//
// Loudly, because a handshake that quietly failed is the most common reason an endpoint cannot be
// registered, and the symptom a user sees is in the dashboard rather than here.
func (s *session) handshook(r *http.Request, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	at := s.deps.Clock.Now()
	if status != http.StatusOK {
		s.note(s.deps.Caps.Glyphs.Handshake, at, "handshake "+s.deps.Caps.Glyphs.Cross, "",
			"not a registration probe, so nothing was echoed")
		return
	}

	s.counts.handshakes++
	challenge := shorten(output.Sanitize(r.URL.Query().Get("hub.challenge")))
	s.note(s.deps.Caps.Glyphs.Handshake, at, "handshake "+s.deps.Caps.Glyphs.OK,
		challenge, "echoed; registration can now succeed")
}
