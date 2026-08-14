Why delivery outcomes arrive as events

Mailkube does not serve past state. A sent message is not queryable, so there is no `emails get`
and no `--wait`: those are not missing features, they are a consequence of the design.

What happened to a message is pushed to you as a webhook event instead. That makes
`mailkube webhooks listen` the way to observe delivery, not a development convenience — and it
is why that command has `--exit-after` and `--exit-timeout`, which turn it into an assertion a
CI job can make.

Three things about registering a local endpoint are worth knowing before you try it:

  * Endpoints must be public https URLs, so a tunnel is required. The CLI does not bundle or
    download one; you pass its URL with --public-url.

  * Registration probes the URL with a challenge request, so the listener has to already be
    running when you register it in the dashboard.

  * Event routing is exclusive. Subscribing a URL to an event type on a domain moves that event
    off whatever endpoint had it, and there is no redelivery. Use a test domain.

For history rather than live events, the dashboard's log view is the place to look.
