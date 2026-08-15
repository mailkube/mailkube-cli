Listing pages, --all, and why a filter matters

A listing returns one page at a time. `--page <n>` reads one page; `--all` walks every page for
you and returns the items together.

  mailkube scheduled-emails list --page 2
  mailkube scheduled-emails list --all --batch-id digest-w33

The two cannot be combined: `--all` means "every page", so naming one is a contradiction rather
than a narrowing, and it is refused instead of guessed at.

--all is for a filtered listing, not for a whole collection

Every page is a request, and requests are rate-limited. Walking a large collection reaches that
limit long before it reaches the client-side ceiling of 1000 items, so the run stops part-way with
a rate-limit error rather than a complete answer. Filter first, then walk:

  mailkube scheduled-emails list --all --batch-id digest-w33
  mailkube scheduled-emails list --all --after 2026-09-01T00:00:00Z --status scheduled

Nothing here retries by itself (see `mailkube topic exit-codes`), so a rate-limited walk exits 7
and leaves the pace to you.

--max-items is client-side

`--max-items` stops the walk after N items. It is not a filter and it is not sent to the server:
the pages it did not need are the pages it does not fetch, so `--max-items 5` over a thousand
matches costs one request. It needs `--all`, because there is nothing to stop otherwise.

When the walk stops early

Reaching the 1000-item ceiling is reported on stderr, naming the count and the flag that raises
it. It is never a silent truncation: a listing that quietly stopped short would be indistinguishable
from a collection that simply ends there, and the difference is the whole answer.
