Sending test mail without spending quota or reputation

There is no sandbox, no test key and no reserved test address. Every send from this tool that is
not a --dry-run is a real, charged, reputation-affecting send. Commands named `test` and flags
named `--sample` do not change that, which is why it is written down here.

What to do instead:

  * --dry-run first, always. It prints exactly what would be sent — the JSON body on the API
    transport, the rendered message on SMTP — and sends nothing.

  * Send to an address you control.

  * Prefer a sending domain that is not the one your production mail goes out on. Bounces and
    complaints attach to the domain that sent them.

  * Note that --sample deliberately generates links and images, which exercises link reputation
    on whatever domain you send from.

  * Both transports send for real. `--transport smtp` is a different protocol, not a test mode.

`mailkube smtp test` submits no message at all: it opens a connection, reads the capabilities, and
stops. Adding --auth also attempts a sign-in and immediately disconnects, which puts a credential
on the wire but never a message. `mailkube doctor` sends nothing on either transport, which is why
it is safe to run as often as you like.
