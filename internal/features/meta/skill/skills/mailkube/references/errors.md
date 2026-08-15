# Error names

Every failed API call carries a machine-readable `name`, a message, an HTTP status and a request
id. `mailkube errors explain <name>` describes any of them, including SMTP codes such as `535`.

Read the `name`, not the message: messages are the server's own text and may be reworded. The
name set is open, so a name this release has never seen still renders and still exits correctly.

The three 403s are not one thing. `invalid_api_key` is a credential failure and exits 3.
`scheduling_not_included` is a plan entitlement and `browser_not_allowed` is a client
precondition; both exit 5, and re-checking the key for either is wasted effort.

`quota_exceeded` is worth knowing about specifically: a suppressed recipient still consumes
quota, because a suppressed address is accepted, charged, and then dropped — visible only as an
`email.suppressed` webhook event.
