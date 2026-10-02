package tgbot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store/db"
)

func (e *env) link(tg int64) {
	e.t.Helper()
	if err := e.st.Q.LinkTg(e.ctx, db.LinkTgParams{UserID: e.user.ID, TgID: tg, CreatedAt: 1}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) expireIn(d time.Duration) {
	e.t.Helper()
	exp := e.clock().Add(d).Unix()
	if _, err := e.st.DB.ExecContext(e.ctx, "UPDATE users SET expires_at = ? WHERE id = ?", exp, e.user.ID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) noticeRows() int {
	e.t.Helper()
	var n int
	if err := e.st.DB.QueryRowContext(e.ctx, "SELECT count(*) FROM tg_notices").Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) sendsTo(chat int64, from int) int {
	e.tg.mu.Lock()
	defer e.tg.mu.Unlock()
	n := 0
	for _, c := range e.tg.calls[min(from, len(e.tg.calls)):] {
		if c.method == "sendMessage" && c.body["chat_id"] == float64(chat) {
			n++
		}
	}
	return n
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	e.now = e.now.Add(d)
	e.mu.Unlock()
}

func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A notice is recorded as sent only once it has been delivered. With Telegram out of reach
// when the round runs, nothing is recorded and the notice comes at a later round, once.
func TestNoticeIsNotLostWhenTelegramIsDown(t *testing.T) {
	e := setup(t, func(e *env, d *Deps) { d.Limits.MaxTries = 1 })
	const anna = 555
	e.link(anna)
	e.expireIn(2 * 24 * time.Hour)
	e.tg.set("sendMessage", true)

	n := e.tg.count()
	e.bot.Notify(e.ctx)
	until(t, "the send is tried", func() bool { return e.sendsTo(anna, n) == 1 })
	time.Sleep(100 * time.Millisecond)
	if e.noticeRows() != 0 {
		t.Fatal("a notice that was not delivered was recorded as sent")
	}

	// The failure is waited out, not hammered at every round.
	e.bot.Notify(e.ctx)
	time.Sleep(100 * time.Millisecond)
	if got := e.sendsTo(anna, n); got != 1 {
		t.Fatalf("a failed notice is sent again at once: %d sends", got)
	}

	e.tg.set("sendMessage", false)
	e.advance(noticeRetry + time.Minute)
	e.bot.Notify(e.ctx)
	until(t, "the notice is recorded", func() bool { return e.noticeRows() == 1 })
	if got := e.sendsTo(anna, n); got != 2 {
		t.Fatalf("delivered at the second round: %d sends", got)
	}

	// And it is done: no third send, now or after a restart.
	e.bot.Notify(e.ctx)
	e.advance(noticeRetry + time.Minute)
	e.bot.Notify(e.ctx)
	time.Sleep(150 * time.Millisecond)
	if got := e.sendsTo(anna, n); got != 2 {
		t.Fatalf("a delivered notice is sent again: %d sends", got)
	}
	restarted := New(e.bot.d)
	restarted.out.Store(e.bot.out.Load())
	restarted.Notify(e.ctx)
	time.Sleep(150 * time.Millisecond)
	if got := e.sendsTo(anna, n); got != 2 {
		t.Fatalf("a restarted panel sends a delivered notice again: %d sends", got)
	}
}

func TestNoticeBookkeeping(t *testing.T) {
	e := setup(t)
	e.link(555)
	n := notice{kind: "expire_1d", period: 100}
	claim := func() bool { _, ok := e.bot.claimNotice(e.ctx, e.user.ID, n, e.clock()); return ok }
	done := func(err error) { e.bot.noticeDone(e.user.ID, n, noticeKey(e.user.ID, n), err) }
	rows := func() int { return e.noticeRows() }

	if !claim() || claim() {
		t.Fatal("a notice on its way is not queued twice")
	}
	done(context.Canceled) // the bot was stopped with it in the queue
	if rows() != 0 || !claim() {
		t.Fatal("a notice dropped by a stop is due again")
	}
	done(ErrUnreachable)
	if rows() != 0 || claim() {
		t.Fatal("a failed notice waits")
	}
	e.advance(noticeRetry)
	if !claim() {
		t.Fatal("and is tried again after the wait")
	}
	done(&APIError{Code: 502, Description: "bad gateway"})
	if rows() != 0 {
		t.Fatal("a failure on Telegram's side is not a delivery")
	}
	e.advance(noticeRetry)
	if !claim() {
		t.Fatal("tried again")
	}
	done(&APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"})
	if rows() != 1 || claim() {
		t.Fatal("a refusal for good is final: nothing to send again")
	}
}

// The pre-checkout answer has Telegram's ten seconds. Whatever else is slow, it goes out
// at once: the update loop does not wait for the other calls to Telegram.
func TestPreCheckoutDoesNotWaitForOtherCalls(t *testing.T) {
	e := setup(t)
	e.tg.mu.Lock()
	e.tg.stall = map[string]time.Duration{"answerCallbackQuery": 2500 * time.Millisecond}
	e.tg.mu.Unlock()
	n := e.tg.count()
	start := time.Now()
	e.press(555, 1, "m")
	e.tg.push(Update{PreCheckoutQuery: &PreCheckoutQuery{ID: "q1", From: User{ID: 555}, Currency: "XTR", TotalAmount: 10, InvoicePayload: "p"}})
	e.tg.until(t, n, func(cs []call) bool { _, ok := find(cs, "answerPreCheckoutQuery"); return ok })
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Fatalf("the pre-checkout waited %s for a callback answer", took)
	}
}

// A Bot API that does not answer is given up on after a while: the call is not held for
// the minute a long poll may take.
func TestCallsHaveTheirOwnTimeout(t *testing.T) {
	var hits atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	c := NewClient(srv.URL, "1:x", nil)
	if c.callTimeout != callTimeout || callTimeout > 20*time.Second {
		t.Fatalf("a call's time: %s", c.callTimeout)
	}
	c.callTimeout = 150 * time.Millisecond
	start := time.Now()
	_, err := c.Send(context.Background(), 1, "hi", nil, false)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("a call that hangs: %v", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("gave up after %s", took)
	}
	if hits.Load() != 1 {
		t.Fatal("the call did not reach the server")
	}
	// A cancelled context stays a cancellation, not an unreachable Telegram.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Send(ctx, 1, "hi", nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	// The pre-checkout answer gets less than other calls: Telegram's ten seconds are short.
	if preCheckoutTimeout >= 10*time.Second || preCheckoutDeadline >= 10*time.Second {
		t.Fatalf("the pre-checkout answer has %s, Telegram gives ten seconds", preCheckoutTimeout)
	}
}

// The saved offset keeps a restart from receiving the last batch again, and a payment
// that could not be applied is offered again, in order, until it is.
func TestOffsetIsSavedAndPaymentsAreRetried(t *testing.T) {
	updateRetryPause = time.Millisecond
	t.Cleanup(func() { updateRetryPause = 2 * time.Second })
	var tries atomic.Int32
	e := setup(t, func(e *env, d *Deps) {
		d.stars = func(_ context.Context, tg int64, payload, charge, currency string, amount int64) error {
			if tries.Add(1) <= 2 {
				return errors.New("database is locked")
			}
			return nil
		}
	})
	pay := Update{Message: &Message{MessageID: 9, From: &User{ID: 555}, Chat: Chat{ID: 555, Type: "private"},
		SuccessfulPayment: &SuccessfulPayment{Currency: "XTR", TotalAmount: 10, InvoicePayload: "p", ChargeID: "ch1"}}}
	e.tg.push(pay)
	e.say(555, "привет") // comes after the payment and waits for it
	n := e.tg.count()
	until(t, "the payment is applied on the third try", func() bool { return tries.Load() == 3 })
	e.tg.wait(t, n, "sendMessage") // the chat message is handled after it
	if tries.Load() != 3 {
		t.Fatalf("an applied payment was offered again: %d tries", tries.Load())
	}
	var rec offsetRecord
	until(t, "the offset is saved", func() bool {
		r, ok, _ := settings.Get[offsetRecord](e.ctx, e.set, KeyOffset)
		rec = r
		return ok && rec.Bot == 1 && rec.Offset >= 1002
	})

	// A restart goes on from there, not from the beginning.
	e.tg.mu.Lock()
	before := len(e.tg.offsets)
	e.tg.mu.Unlock()
	e.bot.Reload()
	until(t, "the restarted bot polls", func() bool {
		e.tg.mu.Lock()
		defer e.tg.mu.Unlock()
		return len(e.tg.offsets) > before+1
	})
	e.tg.mu.Lock()
	first := e.tg.offsets[before:]
	e.tg.mu.Unlock()
	for _, o := range first {
		if o < rec.Offset {
			t.Fatalf("a restarted bot asked from %d, the saved offset is %d: %v", o, rec.Offset, first)
		}
	}
}

// An update that never works is dropped after a few tries, and does not stop the bot.
func TestPoisonUpdateDoesNotStopTheBot(t *testing.T) {
	updateRetryPause = time.Millisecond
	t.Cleanup(func() { updateRetryPause = 2 * time.Second })
	var tries atomic.Int32
	e := setup(t, func(e *env, d *Deps) {
		d.stars = func(context.Context, int64, string, string, string, int64) error {
			tries.Add(1)
			return errors.New("always")
		}
	})
	e.tg.push(Update{Message: &Message{MessageID: 9, From: &User{ID: 555}, Chat: Chat{ID: 555, Type: "private"},
		SuccessfulPayment: &SuccessfulPayment{Currency: "XTR", TotalAmount: 10, InvoicePayload: "p", ChargeID: "ch1"}}})
	n := e.tg.count()
	e.say(555, "привет")
	e.tg.wait(t, n, "sendMessage")
	if got := tries.Load(); got != maxUpdateTries {
		t.Fatalf("tried %d times, want %d", got, maxUpdateTries)
	}
}

// A payment that matches no invoice never will: not offered again.
func TestPaymentWithoutInvoiceIsNotRetried(t *testing.T) {
	updateRetryPause = time.Millisecond
	t.Cleanup(func() { updateRetryPause = 2 * time.Second })
	var tries atomic.Int32
	e := setup(t, func(e *env, d *Deps) {
		d.stars = func(context.Context, int64, string, string, string, int64) error {
			tries.Add(1)
			return billing.ErrBadPayment
		}
	})
	e.tg.push(Update{Message: &Message{MessageID: 9, From: &User{ID: 555}, Chat: Chat{ID: 555, Type: "private"},
		SuccessfulPayment: &SuccessfulPayment{Currency: "XTR", TotalAmount: 10, InvoicePayload: "p", ChargeID: "ch1"}}})
	n := e.tg.count()
	e.say(555, "привет")
	e.tg.wait(t, n, "sendMessage")
	if tries.Load() != 1 {
		t.Fatalf("an unmatched payment was tried %d times", tries.Load())
	}
}

// render is one pass: a value that itself holds {something} is not filled in again, and
// the result does not depend on the order the map is walked in.
func TestRenderIsOnePassAndDeterministic(t *testing.T) {
	vars := map[string]string{"name": "{used} <b>", "used": "5 GB", "brand": "Mikan", "left": "1 GB"}
	want := "Mikan: {used} &lt;b&gt;, 5 GB, {unknown}, {open"
	for range 200 {
		if got := render("{brand}: {name}, {used}, {unknown}, {open", vars); got != want {
			t.Fatalf("render: %q, want %q", got, want)
		}
	}
	if got := render("a < b {brand}", vars); got != "a &lt; b Mikan" {
		t.Fatalf("the text is escaped: %q", got)
	}
}

// The outbox hands out a long queue without copying it for every message.
func TestOutboxQueueIsCheapToDrain(t *testing.T) {
	_, c := newSink(t)
	o := NewOutbox(c, Limits{Total: 1e9, Bulk: 1e9, PerChat: 1e9, Burst: 1e9, Workers: 1, MaxTries: 1}, time.Now, nil, nil)
	const jobs = 60000
	for i := range jobs {
		o.Bulk(int64(i), func(context.Context, *Client) error { return nil }, nil)
	}
	start := time.Now()
	for i := range jobs {
		j, _ := o.next()
		if j == nil || j.chat != int64(i) {
			t.Fatalf("job %d: %+v", i, j)
		}
		o.mu.Lock()
		delete(o.busy, j.chat)
		o.mu.Unlock()
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("draining %d jobs took %s", jobs, took)
	}
	if o.pending(prioBulk) != 0 {
		t.Fatal("the lane is not empty")
	}
}

// A broadcast goes into the outbox in batches, and all of it is sent.
func TestBroadcastIsFedInBatches(t *testing.T) {
	s, c := newSink(t)
	o := NewOutbox(c, fast, time.Now, nil, nil)
	run(t, o)
	b := New(Deps{})
	chats := make([]int64, 1700)
	for i := range chats {
		chats[i] = int64(1000 + i)
	}
	var sentOK atomic.Int32
	var mu sync.Mutex
	most := 0
	queue := func(chat int64) {
		o.Bulk(chat, say(chat, "x"), func(err error) {
			if err == nil {
				sentOK.Add(1)
			}
		})
		mu.Lock()
		most = max(most, o.pending(prioBulk))
		mu.Unlock()
	}
	first := bulkBatch
	for _, ch := range chats[:first] {
		queue(ch)
	}
	b.feed(o, chats[first:], queue)
	s.wait(t, len(chats))
	until(t, "all are sent", func() bool { return int(sentOK.Load()) == len(chats) })
	mu.Lock()
	defer mu.Unlock()
	if most > bulkBatch+bulkLow {
		t.Fatalf("the lane held %d messages at once", most)
	}
}

func TestBroadcastFeedStopsWithTheOutbox(t *testing.T) {
	_, c := newSink(t)
	o := NewOutbox(c, fast, time.Now, nil, nil)
	b := New(Deps{})
	b.bcast = BroadcastProgress{Total: 3}
	ctx, cancel := context.WithCancel(context.Background())
	go o.Run(ctx)
	cancel()
	<-o.Stopped()
	b.feed(o, []int64{1, 2, 3}, func(int64) {})
	if p := b.Progress(); p.Failed != 3 || p.Active() {
		t.Fatalf("what a stopped outbox did not send is failed, and the progress ends: %+v", p)
	}
}

// Run returns only when the poll loop, the outbox and the calls it started have: the
// database is closed after it.
func TestRunWaitsForItsWorkers(t *testing.T) {
	e := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	b := New(e.bot.d)
	returned := make(chan struct{})
	go func() { b.Run(ctx); close(returned) }()
	until(t, "the second bot polls", func() bool { return b.Status().Running })
	if b.out.Load() == nil {
		t.Fatal("no outbox while running")
	}
	cancel()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if b.out.Load() != nil || b.client.Load() != nil {
		t.Fatal("Run returned while the poll loop was still running")
	}
}
