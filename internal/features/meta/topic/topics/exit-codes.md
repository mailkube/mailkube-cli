Exit codes and how to branch on them

Every command returns one of these, and the set is stable within a major version, so
`case $?` is safe to write against.

  0    the command did what was asked
  1    an internal error: a defect in this program, not a problem with your request
  2    usage: the command line itself was wrong, and nothing was attempted
  3    auth: a credential was missing, malformed or rejected
  4    validation: well-formed as a command, invalid as a request
  5    precondition: configuration, entitlement or state
  6    not found
  7    rate limited
  8    the server failed, or answered in a way no client can act on
  9    the server was never reached
  10   reserved for a future bulk verb, where some items succeed and some fail
  124  a deadline you set was reached with no result
  130  interrupted

Two distinctions are worth knowing. 3 is never retried: a rejected credential will not be
accepted a moment later, and repeating it is how an address gets blocked. 9 and 124 are
different questions — 9 means the network failed, 124 means the time you allowed ran out.

On failure stdout is empty, so piping into a parser never yields half a document. `doctor` is
the one exception, and it is a deliberate one: it exits 5 when a check failed, having written
the whole report anyway, because the code is a verdict on the report rather than a failure to
produce one. That is what makes `mailkube doctor && deploy` stop at a broken configuration.
Warnings do not fail it — an unconfigured SMTP credential is not a fault for someone who does
not use SMTP — so `--strict` is there for a caller who wants a clean environment or nothing.

Nothing in this CLI retries by itself. When the server asks for a wait it is reported, and the
decision to wait is left to whatever is running the command.
