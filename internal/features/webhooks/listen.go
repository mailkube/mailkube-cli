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
	// publicURL is the tunnel address to register.
	publicURL string
	// maxBody is the largest delivery accepted.
	maxBody int64
	// filter is the set of event types to handle, empty meaning all of them.
	filter map[string]bool
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

	session := newSession(deps, o, cfg)
	session.banner()
	return session.serve(ctx, socket)
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
	public, err := publicEndpoint(o.publicURL, o.port)
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
	secret := strings.TrimSpace(o.secret)
	if secret == "" {
		if v, ok := deps.Env(settings.EnvWebhookSecret); ok {
			secret = strings.TrimSpace(v)
		}
	}

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

// publicEndpoint checks the tunnel URL, or explains why one is needed.
//
// Requiring it up front rather than at registration time is the point: without a public address
// no event can ever arrive, so a listener that started anyway would sit there looking correct.
func publicEndpoint(raw string, port int) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		local := "http://" + defaultHost + ":" + strconv.Itoa(port)
		return "", errs.Configf(
			"a public URL is required: Mailkube delivers only to public https addresses.\n\n"+
				"Start a tunnel, then pass the URL it gives you:\n"+
				"  cloudflared tunnel --url %s\n"+
				"  ngrok http %d\n\n"+
				"  mailkube webhooks listen --public-url https://<your-url>",
			local, port)
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
