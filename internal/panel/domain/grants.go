package domain

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"mikan/internal/panel/store/db"
)

// Traffic packages (GitHub issue #12): a grant is extra traffic a user got, for the main
// quota or one pool, bought or given by the admin. The period's base quota is spent
// first, then the grants, the soonest to expire first. Grants are spent as the counters
// come in, on the counters' transaction, so a node's batch is spent once; what is left
// of them is added to the quota the nodes get.

// Lifetimes of packages and grants.
const (
	LifetimeUsed   = "used"   // until used up
	LifetimePeriod = "period" // until the end of the traffic period it was given in
	LifetimeDays   = "days"   // N days from when it was given
)

// Sources of grants.
const (
	SourcePurchase = "purchase"
	SourceAdmin    = "admin"
)

// Limits of a package or a grant.
const (
	GiB            = int64(1) << 30
	MinGrantBytes  = GiB
	MaxGrantBytes  = 100 << 40
	MaxGrantDays   = 3650
	maxGrantNoteLn = 200
)

// FieldError is input the domain refuses: Field is the input's name, Code says why.
type FieldError struct{ Field, Code string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Code }

func fieldErr(field, code string) error { return &FieldError{Field: field, Code: code} }

// TrafficLeft is what the user may still use of a quota: the base limit's rest plus the
// active grants. -1: unlimited (no limit), which never touches grants.
func TrafficLeft(limit sql.NullInt64, used, grants int64) int64 {
	if !limit.Valid {
		return -1
	}
	return max(0, limit.Int64-used) + max(0, grants)
}

// Overflow is the part of n new bytes past the base quota, used being what the period
// counted before them: that part is taken from the grants.
func Overflow(limit sql.NullInt64, used, n int64) int64 {
	if !limit.Valid || n <= 0 {
		return 0
	}
	return min(n, max(0, used+n-limit.Int64))
}

// GrantsLeft is what is left of the active grants, by user and pool (0: main traffic).
type GrantsLeft map[[2]int64]int64

func (g GrantsLeft) Main(userID int64) int64         { return g[[2]int64{userID, 0}] }
func (g GrantsLeft) Pool(userID, poolID int64) int64 { return g[[2]int64{userID, poolID}] }

// LoadGrantsLeft reads what is left of every user's active grants.
func LoadGrantsLeft(ctx context.Context, q *db.Queries, now time.Time) (GrantsLeft, error) {
	rows, err := q.SumGrantsLeft(ctx, now.Unix())
	if err != nil {
		return nil, err
	}
	out := GrantsLeft{}
	for _, r := range rows {
		out[[2]int64{r.UserID, r.PoolID}] = r.LeftBytes
	}
	return out, nil
}

// UserGrantsLeft reads what is left of one user's active grants.
func UserGrantsLeft(ctx context.Context, q *db.Queries, userID int64, now time.Time) (GrantsLeft, error) {
	rows, err := q.SumUserGrantsLeft(ctx, db.SumUserGrantsLeftParams{UserID: userID, Now: now.Unix()})
	if err != nil {
		return nil, err
	}
	out := GrantsLeft{}
	for _, r := range rows {
		out[[2]int64{r.UserID, r.PoolID}] = r.LeftBytes
	}
	return out, nil
}

// CountUserTraffic adds a batch of the user's main traffic on q's transaction and takes
// what goes past the base quota from the main grants.
func CountUserTraffic(ctx context.Context, q *db.Queries, userID, up, down int64, now time.Time) error {
	u, err := q.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if err := q.AddUserTraffic(ctx, db.AddUserTrafficParams{Up: up, Down: down, ID: userID}); err != nil {
		return err
	}
	return spendGrants(ctx, q, userID, sql.NullInt64{}, Overflow(u.TrafficLimit, u.UsedUp+u.UsedDown, up+down), now)
}

// CountPoolTraffic adds a batch of the user's traffic in a pool on q's transaction and
// takes what goes past the pool's base quota from the pool's grants.
func CountPoolTraffic(ctx context.Context, q *db.Queries, userID, poolID, up, down int64, now time.Time) error {
	p, err := q.GetUserPool(ctx, db.GetUserPoolParams{UserID: userID, PoolID: poolID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := q.AddUserPoolTraffic(ctx, db.AddUserPoolTrafficParams{UserID: userID, PoolID: poolID, UsedUp: up, UsedDown: down}); err != nil {
		return err
	}
	pool := sql.NullInt64{Int64: poolID, Valid: true}
	return spendGrants(ctx, q, userID, pool, Overflow(p.TrafficLimit, p.UsedUp+p.UsedDown, up+down), now)
}

// spendGrants takes n bytes from the user's active grants for a target (pool NULL: the
// main traffic) in the spending order. What no grant covers is not owed later: the node
// let it through before it learned the quota was out.
func spendGrants(ctx context.Context, q *db.Queries, userID int64, pool sql.NullInt64, n int64, now time.Time) error {
	if n <= 0 {
		return nil
	}
	gs, err := q.ListSpendableGrants(ctx, db.ListSpendableGrantsParams{UserID: userID, PoolID: pool, Now: now.Unix()})
	if err != nil {
		return err
	}
	for _, g := range gs {
		if n == 0 {
			break
		}
		take := min(n, g.Remaining)
		if err := q.SpendGrant(ctx, db.SpendGrantParams{Spent: take, ID: g.ID}); err != nil {
			return err
		}
		n -= take
	}
	return nil
}

// StartPeriod begins a new traffic period on q's transaction: the main and pool counters
// of the period drop to zero and the grants that last one period end. Other grants keep
// what is left of them.
func StartPeriod(ctx context.Context, q *db.Queries, userID, start int64, now time.Time) error {
	if err := q.ResetUserTraffic(ctx, db.ResetUserTrafficParams{PeriodStart: start, UpdatedAt: now.Unix(), ID: userID}); err != nil {
		return err
	}
	if err := q.ResetUserPools(ctx, userID); err != nil {
		return err
	}
	return q.EndPeriodGrants(ctx, db.EndPeriodGrantsParams{Now: now.Unix(), UserID: userID})
}

// GrantSpec is traffic to give a user.
type GrantSpec struct {
	PoolID    int64 // 0: the main traffic
	Bytes     int64
	Lifetime  string
	Days      int64 // LifetimeDays
	Source    string
	PaymentID int64 // 0: none
	PackageID int64 // 0: none
	Note      string
}

// GrantOf is what a package gives when it is bought with payment paymentID.
func GrantOf(p db.TrafficPackage, paymentID int64) GrantSpec {
	return GrantSpec{PoolID: p.PoolID.Int64, Bytes: p.Bytes, Lifetime: p.Lifetime, Days: p.Days, Source: SourcePurchase, PaymentID: paymentID, PackageID: p.ID}
}

// checkGrant validates the size and lifetime of a grant or a package.
func checkGrant(bytes int64, lifetime string, days int64) error {
	switch {
	case bytes < MinGrantBytes || bytes > MaxGrantBytes:
		return fieldErr("bytes", "bad_bytes")
	case lifetime != LifetimeUsed && lifetime != LifetimePeriod && lifetime != LifetimeDays:
		return fieldErr("lifetime", "bad_lifetime")
	case lifetime == LifetimeDays && (days < 1 || days > MaxGrantDays):
		return fieldErr("days", "bad_days")
	}
	return nil
}

// GrantTx gives u traffic on q's transaction. A `period` grant ends with u's traffic
// period (also earlier, when a reset starts a new one); without resets it lasts until a
// reset or until used up.
func GrantTx(ctx context.Context, q *db.Queries, u db.User, g GrantSpec, now time.Time) (db.TrafficGrant, error) {
	var expires sql.NullInt64
	switch g.Lifetime {
	case LifetimeDays:
		expires = sql.NullInt64{Int64: now.Unix() + g.Days*day, Valid: true}
	case LifetimePeriod:
		if t, ok := NextReset(u, now); ok {
			expires = sql.NullInt64{Int64: t.Unix(), Valid: true}
		}
	}
	return q.CreateTrafficGrant(ctx, db.CreateTrafficGrantParams{
		UserID: u.ID, PoolID: sql.NullInt64{Int64: g.PoolID, Valid: g.PoolID != 0}, Bytes: g.Bytes, Remaining: g.Bytes,
		Lifetime: g.Lifetime, ExpiresAt: expires, Source: g.Source,
		PaymentID: sql.NullInt64{Int64: g.PaymentID, Valid: g.PaymentID != 0},
		PackageID: sql.NullInt64{Int64: g.PackageID, Valid: g.PackageID != 0},
		Note:      g.Note, CreatedAt: now.Unix(),
	})
}

// GrantInput is the admin giving a user traffic.
type GrantInput struct {
	PoolID   int64 // 0: the main traffic
	Bytes    int64
	Lifetime string
	Days     int64
	Note     string
}

// Grant gives the user traffic from the admin. A user (or a pool) that ran out gets back
// in at once: the nodes get the bigger quota.
func (s *Users) Grant(ctx context.Context, userID int64, in GrantInput) (db.TrafficGrant, error) {
	in.Note = strings.TrimSpace(in.Note)
	if err := checkGrant(in.Bytes, in.Lifetime, in.Days); err != nil {
		return db.TrafficGrant{}, err
	}
	if len([]rune(in.Note)) > maxGrantNoteLn {
		return db.TrafficGrant{}, fieldErr("note", "note_too_long")
	}
	var g db.TrafficGrant
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		u, err := q.GetUser(ctx, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := poolExists(ctx, q, in.PoolID); err != nil {
			return err
		}
		g, err = GrantTx(ctx, q, u, GrantSpec{PoolID: in.PoolID, Bytes: in.Bytes, Lifetime: in.Lifetime, Days: in.Days, Source: SourceAdmin, Note: in.Note}, s.now())
		return err
	})
	if err != nil {
		return db.TrafficGrant{}, err
	}
	s.changes.PoliciesChanged()
	return g, nil
}

// poolExists: id 0 (the main traffic) or a pool that exists.
func poolExists(ctx context.Context, q *db.Queries, id int64) error {
	if id == 0 {
		return nil
	}
	_, err := q.GetTrafficPool(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return fieldErr("pool_id", "pool_not_found")
	}
	return err
}

// GrantActive: the grant still counts at now.
func GrantActive(g db.TrafficGrant, now time.Time) bool {
	return g.Remaining > 0 && (!g.ExpiresAt.Valid || g.ExpiresAt.Int64 > now.Unix())
}
