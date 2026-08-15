# Driving mailkube from a script

Capture a value and branch on the failure class:

```bash
id=$(mailkube emails send --json @mail.json --idempotency-key auto --jq .id) || case $? in
  3) echo "auth"       >&2; exit 1 ;;
  7) echo "rate limit" >&2; exit 75 ;;   # EX_TEMPFAIL, so the runner retries the step
  *) echo "failed"     >&2; exit 1 ;;
esac
```

Assert delivery in CI by listening for the event the send produces:

```bash
mailkube webhooks listen --public-url "$URL" --secret "$S" \
  --filter email.delivered --exit-after 1 --exit-timeout 60s &
mailkube emails send --json @mail.json
wait $!    # 0 = delivered, 124 = timed out
```

On PowerShell, check `$LASTEXITCODE` rather than trusting the pipeline:

```powershell
$id = mailkube emails send --json '@mail.json' --jq .id
if ($LASTEXITCODE -ne 0) { throw "send failed ($LASTEXITCODE)" }
```

`--exit-after` counts only newly delivered, signature-valid, filter-passing events: a duplicate
redelivery, a filtered-out event and the registration handshake do not count toward it.
