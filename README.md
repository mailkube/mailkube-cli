# Mailkube CLI

[![CI](https://github.com/mailkube/mailkube-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/mailkube/mailkube-cli/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/mailkube/mailkube-cli.svg)](https://pkg.go.dev/github.com/mailkube/mailkube-cli)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Send mail, manage scheduled sends, and receive webhooks from your terminal.
Built for humans, scripts, CI and coding agents.

> **Early development.** The command surface is still being built out and is not yet stable.
> The first stable release will be `v1.0.0`.

## What it does, and what it does not

It covers the send path and the local development loop: sending over the API or SMTP, managing
scheduled sends, and receiving webhook events on your machine.

Domain setup, API keys, SMTP credentials, webhook endpoints and templates are managed in the
dashboard at [app.mailkube.com](https://app.mailkube.com), not here.

There is also no `mailkube emails get`. Mailkube does not serve past state: a message that has
been accepted is not queryable, and delivery outcomes are pushed to you as webhook events instead
of being polled. `mailkube webhooks listen` is how you observe them locally.

## Install

Not yet published. Until the first release, build from source:

```bash
go install github.com/mailkube/mailkube-cli/cmd/mailkube@latest
```

## Output and exit codes

Output is human-readable on a terminal and JSON when piped — no flag needed:

```bash
mailkube version            # human-readable
mailkube version --json     # JSON
mailkube version | cat      # JSON, because stdout is not a terminal
```

**stdout carries the success payload and nothing else.** Progress, warnings, prompts, hints and
errors go to stderr, and on failure stdout is empty, so piping into a parser never yields half a
document.

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | internal error |
| 2 | usage error |

The table grows as commands land; the codes are stable once released.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). `make check` runs everything a pull request must pass.

## Security

See [SECURITY.md](SECURITY.md) to report a vulnerability.

## License

[Apache-2.0](LICENSE).
