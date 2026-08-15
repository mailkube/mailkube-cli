Profiles, the config file, and precedence

Every setting is resolved from four places, in this order:

    flag  >  environment variable  >  config file  >  built-in default

`mailkube config list` shows the effective value of every setting together with which of the
four supplied it, which is how to answer "why is it using that base URL" without guessing.

The file lives in the platform's own configuration directory and holds one or more profiles.
`--config` and MAILKUBE_CONFIG point somewhere else. `mailkube config path` prints the location.

A profile holds two independent credentials: the API key for the REST transport, and an SMTP
username and password for submission. They are different principals, either may be absent, and
neither substitutes for the other — asking for the SMTP transport with no SMTP credential is an
error, never a quiet fallback to REST.

One SMTP credential per profile. More than one sending domain means more than one profile, which
is what `--profile` and `mailkube config profile use` are for.

The file is written atomically under a lock, and created readable only by you. A file that
cannot be parsed is never rewritten or repaired: it may hold the only copy of a credential.
