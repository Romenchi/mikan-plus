package nodesync

import (
	"errors"
	"time"

	"mikan/internal/nodeapi"
)

const (
	retryFirst = 5 * time.Second
	retryMax   = 10 * time.Minute
	// A failure that keeps repeating is logged again after this long, not at every attempt.
	retryRelog = 10 * time.Minute
)

// retry paces the attempts at a node that does not take them: the pause doubles from five
// seconds to ten minutes, and the same error is logged once, not at every attempt for as
// long as the node is down. Not safe for concurrent use; the Syncer guards it.
type retry struct {
	n      int
	until  time.Time
	down   bool // the node did not answer at all, as opposed to refusing what it was sent
	err    string
	logged time.Time
}

func (r *retry) waiting(now time.Time) bool { return now.Before(r.until) }

// unreachable: the node did not answer, so a push of anything else would fail too.
func (r *retry) unreachable(now time.Time) bool { return r.down && r.waiting(now) }

// fail records a failed attempt and says whether it is worth a log line.
func (r *retry) fail(now time.Time, err error) bool {
	r.n++
	d := retryMax
	if r.n <= 8 {
		d = min(retryFirst<<(r.n-1), retryMax)
	}
	r.until = now.Add(d)
	r.down = errors.Is(err, nodeapi.ErrUnavailable)
	if msg := err.Error(); msg != r.err || now.Sub(r.logged) >= retryRelog {
		r.err, r.logged = msg, now
		return true
	}
	return false
}

// ok ends a run of failures and says whether there was one.
func (r *retry) ok() bool {
	was := r.n > 0
	*r = retry{}
	return was
}
