# Voice: how this project writes

Load this when writing anything a user or contributor reads: help text, error messages, README,
documentation, comments, commit messages, issue and PR text.

## Describe Mailkube on its own terms

Documentation, help text and error messages describe **what Mailkube does**. Do not compare
Mailkube to other products, name other vendors, or describe internal infrastructure. Explain the
behaviour a user sees and the action they can take.

This is a writing standard, and it is also the more useful way to write. "Mailkube does not serve
past state; delivery outcomes arrive as webhook events" tells a reader something they can act on.
"Unlike other providers, we don't have a messages endpoint" tells them about someone else.

## Say the consequence, not the mechanism

When a message warns about a risk, name what happens to the user and what they should do instead.
Do not describe the machinery behind it.

- Good: "Repeated malformed sign-ins may get your IP blocked. Not attempting to connect."
- Bad: naming the internal signal, threshold, or subsystem that produces that outcome.

The good version is more actionable *and* shorter. The bad version documents an internal system in
a public artifact and gives an attacker a map.

## Errors carry an action

An error message has three jobs: say what went wrong, say what to do about it, and give the user
something to quote if they need help. A message that only reports failure makes the user guess.

```
✗ invalid_api_key — The API key is not valid.
  Check: mailkube auth status
  request 91bd07c2a4f8…  ·  HTTP 403
```

## Server text passes through unaltered

When the API or an SMTP server supplies a message, render it as given. Do not paraphrase it,
shorten it, or rewrite parts of it. A caller comparing our output against the API's own
documentation must see the same words, and a message we have edited is one we then have to keep in
step forever.

## Plain, specific, unhurried

Prefer the concrete word to the impressive one. Avoid "simply", "just", "easy" and "obviously" —
they tell a stuck reader that their problem is their fault. Write full sentences in prose;
documentation is read by people who are already frustrated.
