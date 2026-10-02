package domain

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mikan/internal/panel/secure"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Every slot is written into every listener, so adding slots recreates the listeners
// and drops all QUIC sessions for 15–30 s (S-02). The pool is therefore large and is
// refilled in big batches, preferably in the quiet hour.
const (
	RefillBatch  = 1024
	LowWatermark = 128 // refill in the quiet hour below this
	CriticalFree = 16  // refill immediately below this
)

type Pool struct {
	st  *store.Store
	now func() time.Time
}

func NewPool(st *store.Store, now func() time.Time) *Pool { return &Pool{st: st, now: now} }

type PoolStats struct {
	Free, Assigned, Burned int64
}

func (p *Pool) Stats(ctx context.Context) (PoolStats, error) {
	rows, err := p.st.Q.CountSlotsByState(ctx)
	if err != nil {
		return PoolStats{}, err
	}
	var s PoolStats
	for _, r := range rows {
		switch r.State {
		case "free":
			s.Free = r.N
		case "assigned":
			s.Assigned = r.N
		case "burned":
			s.Burned = r.N
		}
	}
	return s, nil
}

func (p *Pool) Refill(ctx context.Context, n int) error {
	now := p.now().Unix()
	return p.st.Tx(ctx, func(q *db.Queries) error {
		// Names go on from the last number ever handed out: the largest id alone gives a
		// name back once the slots at the top are purged, and the new slot would inherit the
		// old one's counters on the nodes.
		last, err := q.MaxSlotID(ctx)
		if err != nil {
			return err
		}
		given, err := q.SlotCounter(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		last = max(last, given)
		for i := 1; i <= n; i++ {
			err := q.InsertSlot(ctx, db.InsertSlotParams{Name: fmt.Sprintf("s%06d", last+int64(i)), Uuid: newUUID(), Secret: secure.Token(32), CreatedAt: now})
			if err != nil {
				return err
			}
		}
		return q.SetSlotCounter(ctx, last+int64(n))
	})
}

// PurgeBurned removes burned slots from the listeners; call together with a refill.
func (p *Pool) PurgeBurned(ctx context.Context) error { return p.st.Q.DeleteBurnedSlots(ctx) }

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
