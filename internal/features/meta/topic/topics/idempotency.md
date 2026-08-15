Safe retries, --idempotency-key, and replay

A send is not idempotent by default: run it twice and two messages go out, both charged.

--idempotency-key makes a repeat safe. The server remembers the key and returns the original
response instead of sending again, and the CLI reports that as a replay rather than as a send,
so a script can tell the two apart.

--idempotency-key auto derives the key from the message itself, which matches how the server
binds a key to a body. Re-running the identical command is then a replay.

The consequence that catches people: keys are remembered for 24 hours. A send you deliberately
want to repeat inside that window — the same message, twice, on purpose — becomes a silent
no-op under `auto`. Vary the body, or pass your own key.

A key and a different body is a conflict, not a replay, and exits 5.
