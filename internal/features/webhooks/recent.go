package webhooks

import "time"

// recent remembers the most recently seen keys and when each was first seen.
//
// It is bounded on purpose. The platform retries a delivery up to fifteen times with a stable id,
// so deduplication needs memory, but a listener left running for a week must not grow one entry
// per event forever. Evicting the oldest is the right trade here: retries of a delivery arrive
// within hours of the original, so an id old enough to be evicted is old enough that a repeat of
// it is not a retry.
//
// Not safe for concurrent use. The one caller holds a mutex across the whole of a delivery, which
// is also what keeps two events from interleaving halfway through a rendered line.
type recent struct {
	// limit is how many keys are kept.
	limit int
	// at maps a key to when it was first added.
	at map[string]time.Time
	// order is insertion order, oldest first, so eviction does not have to search.
	order []string
}

// newRecent returns a store holding at most limit keys.
func newRecent(limit int) *recent {
	return &recent{limit: limit, at: make(map[string]time.Time, limit)}
}

// add records a key, reporting whether it was already known and when it was first seen.
//
// The first-seen time is returned rather than discarded because both callers want it: one to
// recognise a redelivery, the other to say how long a message took to reach its outcome.
func (r *recent) add(key string, now time.Time) (first time.Time, seen bool) {
	if at, known := r.at[key]; known {
		return at, true
	}

	r.at[key] = now
	r.order = append(r.order, key)
	if len(r.order) > r.limit {
		delete(r.at, r.order[0])
		r.order = r.order[1:]
	}
	return now, false
}
