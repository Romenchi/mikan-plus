package tgbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mikan/internal/panel/domain"
	"mikan/internal/panel/store/db"
)

// A notice goes once per subscription term or traffic period (tg_notices).
type notice struct {
	kind   string
	period int64
	text   string
}

// due are the notices a subscription has earned now; grants is what is left of its main
// traffic packages: traffic runs out only when they do too.
func due(u db.User, grants int64, now time.Time, n Notify) []notice {
	if u.Status == "disabled" {
		return nil
	}
	var out []notice
	if u.ExpiresAt.Valid {
		exp := time.Unix(u.ExpiresAt.Int64, 0)
		left := exp.Sub(now)
		switch {
		case left <= 0 && left > -3*24*time.Hour && n.Expired:
			out = append(out, notice{kind: "expired", period: u.ExpiresAt.Int64})
		case left > 0 && left <= 24*time.Hour && n.Expire1d:
			out = append(out, notice{kind: "expire_1d", period: u.ExpiresAt.Int64})
		case left > 24*time.Hour && left <= 3*24*time.Hour && n.Expire3d:
			out = append(out, notice{kind: "expire_3d", period: u.ExpiresAt.Int64})
		}
	}
	if u.TrafficLimit.Valid && u.TrafficLimit.Int64 > 0 {
		used := u.UsedUp + u.UsedDown
		switch {
		case domain.TrafficLeft(u.TrafficLimit, used, grants) == 0 && n.Traffic100:
			out = append(out, notice{kind: "traffic_100", period: u.PeriodStart})
		case grants <= 0 && used*10 >= u.TrafficLimit.Int64*9 && n.Traffic90:
			out = append(out, notice{kind: "traffic_90", period: u.PeriodStart})
		}
	}
	return out
}

// What a notice has been through in this process. tg_notices holds only the notices that
// were delivered, so a notice that was not (Telegram unreachable, the outbox stopped by a
// reload or a restart) is still due at the next round and goes again; this keeps one that
// is on its way, or has just failed, from being queued twice meanwhile.
type noticeState struct {
	queued  bool      // in the outbox
	retryAt time.Time // delivery failed: not again before this
	sent    bool      // delivered, but the row could not be written
}

// noticeRetry is how long a notice waits after a failed delivery.
const noticeRetry = 5 * time.Minute

func noticeKey(userID int64, n notice) string {
	return fmt.Sprintf("%d/%s/%d", userID, n.kind, n.period)
}

// claimNotice says whether n goes out now: not delivered before, not on its way, not
// waiting out a failure.
func (b *Bot) claimNotice(ctx context.Context, userID int64, n notice, now time.Time) (string, bool) {
	key := noticeKey(userID, n)
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	if st := b.notices[key]; st != nil && (st.queued || st.sent || now.Before(st.retryAt)) {
		return "", false
	}
	var one int
	err := b.d.Store.DB.QueryRowContext(ctx, `SELECT 1 FROM tg_notices WHERE user_id = ? AND kind = ? AND period = ?`, userID, n.kind, n.period).Scan(&one)
	switch {
	case err == nil:
		return "", false // delivered
	case !errors.Is(err, sql.ErrNoRows):
		b.d.Log.Error("telegram: notices", "err", err)
		return "", false
	}
	b.notices[key] = &noticeState{queued: true}
	return key, true
}

// noticeDone is the outbox's word on a notice. Delivered, or refused for good (the user
// blocked the bot, the chat is gone: sending again changes nothing), it is recorded and
// never comes again. Anything else leaves it due for the next round.
func (b *Bot) noticeDone(userID int64, n notice, key string, err error) {
	var ae *APIError
	// A refusal below 500 is Telegram's final word; a failure on its side is not.
	delivered := err == nil || errors.As(err, &ae) && ae.Code < 500
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	st := b.notices[key]
	if st == nil {
		return
	}
	if !delivered {
		if errors.Is(err, context.Canceled) {
			delete(b.notices, key) // the bot was stopped: the next round after it starts sends it
			return
		}
		st.queued, st.retryAt = false, b.d.Now().Add(noticeRetry)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, werr := b.d.Store.Q.AddTgNotice(ctx, db.AddTgNoticeParams{UserID: userID, Kind: n.kind, Period: n.period, SentAt: b.d.Now().Unix()}); werr != nil {
		b.d.Log.Error("telegram: notice sent, but not recorded", "err", werr)
		st.queued, st.sent = false, true
		return
	}
	delete(b.notices, key)
}

// forgetNotices drops what is no longer of use: failures that have waited long enough.
func (b *Bot) forgetNotices(now time.Time) {
	b.noticeMu.Lock()
	defer b.noticeMu.Unlock()
	for k, st := range b.notices {
		if !st.queued && !st.sent && !now.Before(st.retryAt) {
			delete(b.notices, k)
		}
	}
}

// night: automatic notices then come without a sound. Most users live on Moscow time;
// a notice at 3 a.m. with a ring is how a bot gets reported as spam.
func night(t time.Time) bool {
	h := t.In(time.FixedZone("MSK", 3*60*60)).Hour()
	return h >= 22 || h < 9
}

// Notify queues the notices that are due, each until it is delivered, and not twice at
// once. The outbox paces them behind the replies to people in their chats.
func (b *Bot) Notify(ctx context.Context) {
	out := b.out.Load()
	if out == nil {
		return
	}
	cfg := b.Config(ctx)
	w := wordsFor(cfg.Lang)
	now := b.d.Now()
	silent := cfg.QuietNight && night(now)
	b.forgetNotices(now)
	links, err := b.d.Store.Q.ListTgLinks(ctx)
	if err != nil {
		b.d.Log.Error("telegram: notices", "err", err)
		return
	}
	for _, l := range links {
		if l.Blocked.Valid && l.Blocked.Int64 != 0 {
			continue
		}
		u, err := b.d.Store.Q.GetUser(ctx, l.UserID)
		if err != nil {
			continue
		}
		grants, err := domain.UserGrantsLeft(ctx, b.d.Store.Q, u.ID, now)
		if err != nil {
			continue
		}
		for _, n := range due(u, grants.Main(u.ID), now, cfg.Notify) {
			key, ok := b.claimNotice(ctx, u.ID, n, now)
			if !ok {
				continue
			}
			vars := b.vars(ctx, w, u, now)
			text := render(map[string]string{"expire_3d": pick(cfg.Texts.Expiring, w.expiring), "expire_1d": pick(cfg.Texts.Expiring, w.expiring),
				"expired": pick(cfg.Texts.Expired, w.expired), "traffic_90": pick(cfg.Texts.Traffic90, w.traffic90), "traffic_100": pick(cfg.Texts.TrafficEnd, w.trafficEnd)}[n.kind], vars)
			var kb *Keyboard
			if n.kind != "traffic_90" {
				kb = &Keyboard{[][]Button{{{Text: labelOf(cfg, "renew", "💳"), CallbackData: "r"}}}}
			}
			chat, userID := l.TgID, u.ID
			out.Notice(chat, func(ctx context.Context, c *Client) error {
				_, err := c.Send(ctx, chat, text, kb, silent)
				return err
			}, func(err error) { b.noticeDone(userID, n, key, err) })
		}
	}
	if now.Hour() == 3 && now.Minute() < 10 {
		_ = b.d.Store.Q.PruneTgNotices(ctx, now.Add(-90*24*time.Hour).Unix())
	}
}

var (
	ErrBusy = errors.New("broadcast_busy")
	ErrOff  = errors.New("bot_off")
)

// Broadcast queues the admin's text for every account with a subscription and returns
// how many will get it. The outbox sends it at 20 messages a second at most, after the
// replies and the notices; the admin panel shows the progress.
func (b *Bot) Broadcast(ctx context.Context, text string) (int, error) {
	out := b.out.Load()
	if out == nil {
		return 0, ErrOff
	}
	b.mu.Lock()
	if b.bcast.Active() {
		b.mu.Unlock()
		return 0, ErrBusy
	}
	targets, err := b.d.Store.Q.BroadcastTargets(ctx)
	if err != nil {
		b.mu.Unlock()
		return 0, err
	}
	b.bcast = BroadcastProgress{Total: len(targets), Started: b.d.Now()}
	b.mu.Unlock()
	body := render(text, map[string]string{"brand": b.brand(ctx)})
	queue := func(chat int64) {
		out.Bulk(chat, func(ctx context.Context, c *Client) error {
			_, err := c.Send(ctx, chat, body, nil, false)
			return err
		}, func(err error) {
			b.mu.Lock()
			if err == nil {
				b.bcast.Sent++
			} else {
				b.bcast.Failed++
			}
			b.mu.Unlock()
		})
	}
	// The outbox sends 20 a second; queueing everyone at once would keep a message and a
	// closure per account in memory for as long as that takes. A first batch goes in now,
	// the rest as the lane drains.
	first := min(len(targets), bulkBatch)
	for _, chat := range targets[:first] {
		queue(chat)
	}
	if rest := targets[first:]; len(rest) > 0 {
		go b.feed(out, rest, queue)
	}
	return len(targets), nil
}

// bulkBatch is how many broadcast messages wait in the outbox; feed tops the lane up when
// fewer than bulkLow are left.
const (
	bulkBatch = 500
	bulkLow   = 100
)

// feed queues the rest of a broadcast in batches as the outbox sends. If the outbox stops
// (the bot is reloaded or switched off) what is left is counted as failed, so the
// progress ends.
func (b *Bot) feed(out *Outbox, rest []int64, queue func(chat int64)) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for len(rest) > 0 {
		select {
		case <-out.Stopped():
			b.mu.Lock()
			b.bcast.Failed += len(rest)
			b.mu.Unlock()
			return
		case <-t.C:
		}
		if out.pending(prioBulk) > bulkLow {
			continue
		}
		n := min(len(rest), bulkBatch)
		for _, chat := range rest[:n] {
			queue(chat)
		}
		rest = rest[n:]
	}
}

// Progress is the last broadcast.
func (b *Bot) Progress() BroadcastProgress {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bcast
}

// MiniAppUser checks the Mini App's initData and returns that Telegram account's id and
// its subscriptions, the one the chat shows first.
func (b *Bot) MiniAppUser(ctx context.Context, initData string) (int64, []db.User, error) {
	token, err := b.d.Settings.String(ctx, KeyToken)
	if err != nil || token == "" {
		return 0, nil, ErrOff
	}
	tu, err := CheckInitData(token, initData, b.d.Now())
	if err != nil {
		return 0, nil, err
	}
	list, err := b.d.Store.Q.ListTgLinksOf(ctx, tu.ID)
	if err != nil {
		return 0, nil, err
	}
	if ch, err := b.d.Store.Q.GetTgChat(ctx, tu.ID); err == nil {
		for i, u := range list {
			if u.ID == ch.Current && i > 0 {
				list[0], list[i] = list[i], list[0]
			}
		}
	}
	return tu.ID, list, nil
}
