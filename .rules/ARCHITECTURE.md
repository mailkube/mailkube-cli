# Architecture: feature modules over a shared kernel

Load this when adding or changing a command, adding a package, or touching `internal/cli`.

## The shape

```
cmd/mailkube/main.go        the only place the process ends
        │
internal/cli/               composition root + registry
        │
internal/features/<name>/   one module per capability   → may use kernel + SDK
        │                                                 never another feature
internal/kernel/<name>/     shared primitives           → never uses a feature
```

Adding a capability is **one new directory plus one line in `internal/cli/registry.go`**. It is
never an edit to an existing feature, a switch statement, or a second list of commands. If you
find yourself editing something else to make a new command appear, the change is in the wrong
place.

## The dependency law

Four rules, enforced by `depguard` in `.golangci.yml` rather than by review. An architecture that
depends on people remembering it is a diagram, not an architecture.

1. **A feature never imports another feature.** Share through the kernel. Two features that know
   about each other turn the registry into a dependency graph, and the next person cannot add a
   command without reading all of them.
2. **The kernel never imports a feature.** If it needs something a feature has, invert it with an
   interface declared in the kernel and implemented by the feature.
3. **`net/smtp` is denied everywhere.** SMTP lives in the SDK's `smtp` subpackage. A second
   implementation of a wire format is how two of them start disagreeing.
4. **`net/http` is denied outside four places**: `kernel/clientfactory` (which constructs the
   `*http.Client` handed to the SDK), `kernel/tunnel`, `features/webhooks` (which serves a local
   listener), and the `version` and `doctor` meta features (which probe). Anywhere else, an
   `net/http` import means a REST call that went around the SDK.

`depguard` denies **direct** imports only. Pulling `net` in transitively through `net/http` is
expected and is not a violation.

**The CLI calls the API only through `github.com/mailkube/mailkube-go`.** There is no second HTTP
path, no hand-rolled request, and no URL literal outside `clientfactory`. When the API gains
something the CLI needs, it goes into the SDK first.

## The testability seams

Each of these has exactly one home, so a test can substitute it. `forbidigo` denies the direct
call everywhere else, because "we will remember to inject it" does not survive the third
contributor.

| Seam | Lives in | Denied elsewhere |
|---|---|---|
| Output streams | `kernel/feature.IOStreams` | `fmt.Print*`, `os.Stdout/Stderr/Stdin` |
| Logging | the injected `slog` handler | `log.Print*`, `log.Fatal*` |
| Time | the injected clock | `time.Now` |
| Ending the process | `cmd/mailkube/main.go` | `os.Exit` |

Two cobra rules follow from the same idea: build the command tree with `NewRootCmd(*Deps)` rather
than a package-level `var rootCmd` (also enforced by `gochecknoglobals`), and have every `RunE`
return an error instead of printing one. Cobra commands hold parsed flag state, so a shared tree
leaks one invocation's flags into the next — invisible in production, where the process runs once,
and maddening in tests, where it does not.

## The stream contract

**stdout carries the success payload and nothing else.** Progress, warnings, prompts, hints and
errors all go to stderr, and on failure stdout stays empty so a caller piping into a parser never
sees half a document. Golden tests capture the two streams separately, which is what stops this
contract from breaking silently.

## Local SDK co-development

The committed `go.mod` must resolve the SDK from the module proxy, or
`go install github.com/mailkube/mailkube-cli@latest` breaks for everyone outside this workspace.
To work against an unreleased SDK, use a **gitignored `go.work`**:

```
go 1.24

use (
	.
	../mailkube-go
)
```

Never add a `replace` directive to `go.mod`.

## Golden files

Every screen a user sees is committed under a feature's `testdata/golden/`. Regenerate with
`make golden`, then **read the diff** — a golden regenerated and committed unread is worse than no
golden, because it converts a behaviour change into a silent one.
