package webhooks

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	mailkube "github.com/mailkube/mailkube-go"

	"github.com/mailkube/mailkube-cli/internal/kernel/errs"
	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/input"
	"github.com/mailkube/mailkube-cli/internal/kernel/output"
	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
	"github.com/mailkube/mailkube-cli/internal/kernel/settings"
)

// config is the settled form of the flags: everything checked, resolved and ready to serve.
//
// It exists so that every refusal happens before a socket is opened. A listener that binds, prints
// a banner, and only then discovers it has no signing secret has already told the user it is
// working.
type config struct {
	// address is what to bind.
	address string
	// path is where deliveries arrive.
	path string
	// secret verifies deliveries. Empty only when the user asked for no verification.
	secret string
	// publicURL is the tunnel address to register, empty when running --local.
	publicURL string
	// maxBody is the largest delivery accepted.
	maxBody int64
	// filter is the set of event types to handle, empty meaning all of them.
	filter map[string]bool
	// forward is where each accepted delivery is re-posted, empty when nowhere.
	forward string
}

// run binds, announces, and serves until something says to stop.
func (f *Feature) run(ctx context.Context, deps *feature.Deps, o *options) error {
	cfg, err := f.settle(deps, o)
	if err != nil {
		return err
	}

	// The socket comes before the banner, because a banner reading "Listening" above an
	// "address already in use" is a worse report than the error alone. tcp4 and a literal
	// address are both deliberate: "localhost" also resolves to ::1, and a listener that
	// answered on one family while the tunnel connected to the other looks like a delivery
	// that never arrives.
	socket, err := f.bind(cfg.address)
	if err != nil {
		return errs.Configf("cannot listen on %s: %s.\nPass --port to use another.", cfg.address, bindReason(err))
	}

	session, err := newSession(deps, o, cfg)
	if err != nil {
		_ = socket.Close()
		return err
	}

	session.banner()
	return session.serve(ctx, socket)
}

// loopback reports whether an address reaches only this machine.
//
// It parses rather than compares, because "127.0.0.1", "::1" and "127.0.0.2" are all this machine
// and a string match would accept the first and refuse the others for no reason a user could
// guess. A name other than "localhost" is not resolved: a lookup here would mean a --forward guard
// whose answer depends on the DNS server, which is not a guard.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// bind opens the socket, real unless a test substituted one.
func (f *Feature) bind(address string) (net.Listener, error) {
	if f.Bind != nil {
		return f.Bind(address)
	}
	return net.Listen("tcp4", address)
}

// bindReason reduces a listen failure to the part a user can act on.
//
// Go wraps it as "listen tcp4 127.0.0.1:4318: bind: address already in use", which repeats the
// address the message is about to name again.
func bindReason(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return opErr.Err.Error()
	}
	return err.Error()
}

// settle turns the flags into a config, refusing anything that cannot work.
func (f *Feature) settle(deps *feature.Deps, o *options) (config, error) {
	if err := checkRendering(deps, o); err != nil {
		return config{}, err
	}

	address, err := bindAddress(o.host, o.port)
	if err != nil {
		return config{}, err
	}
	path, err := deliveryPath(o.path)
	if err != nil {
		return config{}, err
	}
	secret, err := resolveSecret(deps, o)
	if err != nil {
		return config{}, err
	}
	public, err := publicEndpoint(o)
	if err != nil {
		return config{}, err
	}
	forward, err := forwardTarget(o)
	if err != nil {
		return config{}, err
	}
	maxBody, err := input.ParseSize(o.maxBody)
	if err != nil {
		return config{}, err
	}

	return config{
		address:   address,
		path:      path,
		secret:    secret,
		publicURL: public,
		maxBody:   maxBody,
		filter:    filterSet(deps, o.filter),
		forward:   forward,
	}, nil
}

// checkRendering refuses the output combinations that cannot describe a stream.
func checkRendering(deps *feature.Deps, o *options) error {
	switch o.print {
	case printPretty, printRaw, printSummary:
	default:
		return errs.Usagef("unknown --print %q: use pretty, raw or summary", o.print)
	}

	// A stream has no end, and a YAML document set needs separators the encoder can only write
	// when it knows how many documents there are. Refusing is kinder than emitting something
	// that looks like YAML and does not parse as one stream.
	if deps.Format == output.YAML {
		return errs.Usagef(
			"`webhooks listen` streams events, and YAML has no line-oriented form.\nUse -o ndjson instead.")
	}
	if o.maxField < minFieldWidth {
		return errs.Usagef("--max-field must be at least %d: a narrower column shows nothing", minFieldWidth)
	}
	return nil
}

// bindAddress composes the listen address, refusing a port that is not one.
func bindAddress(host string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", errs.Usagef("%d is not a usable port", port)
	}
	if strings.TrimSpace(host) == "" {
		return "", errs.Usagef("--host cannot be empty")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// deliveryPath checks the path deliveries arrive on.
func deliveryPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if !strings.HasPrefix(trimmed, "/") {
		return "", errs.Usagef("--path must begin with /, as in /webhooks")
	}
	return trimmed, nil
}

// resolveSecret finds the signing secret, or refuses to run without one.
//
// Refusing is the whole posture. An endpoint URL is public and anyone may POST to it, so the
// signature is the only thing separating a real delivery from a stranger's, and a tool whose
// default is to skip that check teaches a habit that carries into the user's own handler.
func resolveSecret(deps *feature.Deps, o *options) (string, error) {
	secret := secretFrom(deps, o.secret)

	switch {
	case secret != "" && o.skipVerify:
		return "", errs.Usagef("--skip-verify and a signing secret ask for opposite things. Pass one or the other.")
	case secret != "":
		return secret, nil
	case o.skipVerify:
		return "", nil
	}

	return "", errs.Configf(
		"no signing secret. Pass --secret or set %s.\n"+
			"The secret is shown beside the endpoint at %s.\n"+
			"Pass --skip-verify to accept unverified events; every one of them is badged, and it\n"+
			"is not a mode to develop against for long.",
		settings.EnvWebhookSecret, routes.Dashboard("/domain/webhooks"))
}

// secretFrom resolves a signing secret from the flag, then the environment.
//
// One place, because all three commands here take a secret and a fourth reading of the same
// variable is how one of them ends up honouring a spelling the others do not. The secret is never
// read from the config file: it belongs to one endpoint rather than to a profile, and a developer
// usually holds several at once, so storing one would make the wrong one the default.
func secretFrom(deps *feature.Deps, flag string) string {
	if secret := strings.TrimSpace(flag); secret != "" {
		return secret
	}
	if fromEnv, ok := deps.Env(settings.EnvWebhookSecret); ok {
		return strings.TrimSpace(fromEnv)
	}
	return ""
}

// publicEndpoint checks the tunnel URL, or explains why one is needed.
//
// Requiring it up front rather than at registration time is the point: without a public address no
// event can ever arrive, so a listener that started anyway would sit there looking correct. The
// CLI never starts a tunnel itself, so this string is the only way a public address enters the
// program.
func publicEndpoint(o *options) (string, error) {
	value := strings.TrimSpace(o.publicURL)

	switch {
	case o.local && value != "":
		return "", errs.Usagef("--local and --public-url ask for opposite things. Pass one or the other.")
	case o.local:
		// Deliberately empty. --local is the caller saying there is no tunnel, which is
		// true for a loop driven by `webhooks simulate` or by a replayed capture.
		return "", nil
	case value == "":
		return "", errs.Configf(
			"a public URL is required: Mailkube delivers only to public https addresses.\n\n"+
				"Start a tunnel, then pass the URL it gives you:\n"+
				"  cloudflared tunnel --url http://%s:%d\n"+
				"  ngrok http %d\n\n"+
				"  mailkube webhooks listen --public-url https://<your-url>\n\n"+
				"Or pass --local to receive only what you post yourself, with `webhooks simulate`.",
			defaultHost, o.port, o.port)
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", errs.Configf("%q is not a URL", value)
	}
	if parsed.Scheme != "https" {
		return "", errs.Configf(
			"%q is not an https URL, and Mailkube delivers only over https.\n"+
				"A tunnel gives you an https address even though this listener speaks plain HTTP.", value)
	}
	return value, nil
}

// forwardTarget checks where deliveries are re-posted, and refuses to shout at a stranger.
//
// A forward is this machine re-sending someone else's signed payload, so the default target has to
// be this machine. Pointing it at a public address turns a development tool into a relay, and the
// mistake that does it is a typo in a hostname rather than a decision anyone made.
func forwardTarget(o *options) (string, error) {
	value := strings.TrimSpace(o.forward)
	if value == "" {
		return "", nil
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errs.Usagef("--forward %q is not an http or https URL", value)
	}
	if !o.force && !loopback(parsed.Hostname()) {
		return "", errs.Usagef(
			"--forward %q is not on this machine.\n"+
				"Forwarding re-sends a signed payload, so the target is loopback unless you pass --force.",
			value)
	}
	return value, nil
}

// filterSet turns the requested event types into a lookup, mentioning any this release cannot name.
//
// An unrecognised type is reported and then honoured rather than refused. The platform may emit an
// event type newer than this binary, and filtering for one is exactly what someone investigating a
// new event would want to do; rejecting it would make the CLI's own age the user's problem.
func filterSet(deps *feature.Deps, requested []string) map[string]bool {
	if len(requested) == 0 {
		return nil
	}

	known := make(map[string]bool, len(mailkube.EventTypes()))
	for _, eventType := range mailkube.EventTypes() {
		known[eventType] = true
	}

	filter := make(map[string]bool, len(requested))
	for _, eventType := range requested {
		trimmed := strings.TrimSpace(eventType)
		if trimmed == "" {
			continue
		}
		if !known[trimmed] {
			deps.Progress("%s this release does not know the event type %q; filtering for it anyway",
				deps.Caps.Glyphs.Warn, output.Sanitize(trimmed))
		}
		filter[trimmed] = true
	}
	return filter
}
