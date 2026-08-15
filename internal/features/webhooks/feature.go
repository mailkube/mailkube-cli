// Package webhooks implements `mailkube webhooks`.
//
// Mailkube does not serve past state: a sent message is not queryable, and what happened to it
// arrives as an event pushed to an endpoint. That makes this package the product's observability
// mechanism rather than a convenience, which is why `listen` is built to be asserted on in CI as
// well as watched by a person.
//
// Three platform properties shape the command, and each one is a support question if the tool
// hides it. Endpoints must be public https addresses, so a tunnel is not optional. Registration
// probes the endpoint with a challenge it must echo, so the listener has to already be running
// when the endpoint is created. And routing is exclusive: subscribing a development URL to an
// event type on a production domain moves that event away from production, with no redelivery.
package webhooks

import (
	"net"
	"time"

	"github.com/spf13/cobra"

	"github.com/mailkube/mailkube-cli/internal/kernel/feature"
	"github.com/mailkube/mailkube-cli/internal/kernel/routes"
)

// The listener's defaults.
//
// The port is high enough to need no privilege and is not one a development server is likely to
// have taken; 5000 in particular is unusable on macOS, where the system already answers on it.
const (
	defaultHost      = "127.0.0.1"
	defaultPort      = 4318
	defaultPath      = "/"
	defaultTolerance = 300 * time.Second
	defaultMaxBody   = "1MiB"
	defaultMaxField  = 512
)

// The rendering modes `--print` selects.
const (
	// printPretty is the badge-per-event human form.
	printPretty = "pretty"
	// printRaw prints the delivered body rather than this program's reading of it.
	printRaw = "raw"
	// printSummary shows only the closing counts, for a run nobody is watching.
	printSummary = "summary"
)

// Bind opens the socket the listener serves on. The seam a test substitutes.
//
// A test cannot take a fixed port: the suite runs in parallel, and a port that is free on one
// machine is taken on another, which is how a listener test becomes the flaky one nobody trusts.
// Substituting the bind lets a test listen on whatever the operating system offers and still
// exercise the real server, the real handshake and a real HTTP request.
type Bind func(address string) (net.Listener, error)

// Feature receives webhook events on this machine.
type Feature struct {
	// Bind opens the socket. Nil means the real one.
	Bind Bind
}

// New returns the webhooks feature.
func New() *Feature { return &Feature{} }

// Name implements feature.Feature.
func (*Feature) Name() string { return "webhooks" }

// HelpEntries implements feature.Listed.
func (*Feature) HelpEntries() []feature.Entry {
	return []feature.Entry{{
		Group:      feature.GroupDevelop,
		Invocation: "webhooks listen",
		Summary:    "Receive webhook events on your machine",
	}}
}

// Command implements feature.Feature.
func (f *Feature) Command(deps *feature.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhooks",
		Short: "Receive and inspect webhook events",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(f.listenCmd(deps))
	cmd.AddCommand(managedElsewhere()...)
	return cmd
}

// managedElsewhere answers the webhook verbs that are real capabilities of the product and are
// not capabilities of this tool.
//
// Endpoints and their signing secrets are created in the dashboard. Someone typing
// `mailkube webhooks create` has made a reasonable guess, and answering "unknown command" tells
// them the thing does not exist when in fact they are one page away from it. This is the one
// command where that guess is especially likely, because `webhooks listen` does exist and
// establishes the noun.
//
// Hidden, because these are answers to a wrong guess rather than capabilities to advertise.
func managedElsewhere() []*cobra.Command {
	area, known := routes.AreaFor("webhooks")
	if !known {
		return nil
	}

	verbs := []string{"create", "list", "get", "update", "delete"}
	commands := make([]*cobra.Command, 0, len(verbs))
	for _, verb := range verbs {
		commands = append(commands, &cobra.Command{
			Use:    verb,
			Short:  area.Summary + ", managed in the dashboard",
			Hidden: true,
			// Arbitrary arguments and tolerated unknown flags, because someone types
			// `webhooks delete https://…` or `webhooks list --json`, and failing on the
			// shape of the guess answers a question they did not ask.
			Args:               cobra.ArbitraryArgs,
			FParseErrWhitelist: cobra.FParseErrWhitelist{UnknownFlags: true},
			RunE: func(c *cobra.Command, _ []string) error {
				return routes.Refer("webhooks "+c.Name(), area)
			},
		})
	}
	return commands
}

// options is everything `webhooks listen` accepts.
type options struct {
	host        string
	port        int
	path        string
	publicURL   string
	secret      string
	tolerance   time.Duration
	skipVerify  bool
	maxBody     string
	maxField    int
	filter      []string
	print       string
	exitAfter   int
	exitTimeout time.Duration
}

// listenCmd builds `webhooks listen`.
func (f *Feature) listenCmd(deps *feature.Deps) *cobra.Command {
	o := &options{}

	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Receive webhook events on this machine",
		Long: "Receive webhook events on this machine.\n\n" +
			"Mailkube delivers only to public https addresses, so run a tunnel and pass its\n" +
			"URL with --public-url. Keep this running while you register the endpoint: the\n" +
			"platform probes the URL with a challenge before it will save it.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return f.run(c.Context(), deps, o)
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&o.host, "host", defaultHost, "address to bind")
	fs.IntVar(&o.port, "port", defaultPort, "port to bind")
	fs.StringVar(&o.path, "path", defaultPath, "path deliveries arrive on")
	fs.StringVar(&o.publicURL, "public-url", "", "the tunnel URL this listener is reachable at")
	fs.StringVar(&o.secret, "secret", "", "endpoint signing secret; MAILKUBE_WEBHOOK_SECRET is read too")
	fs.DurationVar(&o.tolerance, "tolerance", defaultTolerance, "accepted clock skew on a delivery timestamp")
	fs.BoolVar(&o.skipVerify, "skip-verify", false, "accept unverified deliveries (development only)")
	fs.StringVar(&o.maxBody, "max-body", defaultMaxBody, "largest delivery body accepted")
	fs.IntVar(&o.maxField, "max-field", defaultMaxField, "widest rendered field before it is shortened")
	fs.StringSliceVar(&o.filter, "filter", nil, "only handle these event types")
	fs.StringVar(&o.print, "print", printPretty, "human rendering: pretty, raw or summary")
	fs.IntVar(&o.exitAfter, "exit-after", 0, "stop once this many matching events have arrived")
	fs.DurationVar(&o.exitTimeout, "exit-timeout", 0, "stop with exit 124 if that has not happened in time")
	return cmd
}
