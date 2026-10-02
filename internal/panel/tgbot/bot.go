package tgbot

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/domain"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/store/db"
)

// Settings keys of the bot.
const (
	KeyEnabled = "tg_enabled"
	KeyToken   = "tg_token" // never leaves the panel's API
	KeyBot     = "tg_bot"   // the token's bot, from getMe
	KeyConfig  = "tg_config"
	KeySecret  = "tg_secret" // signs the link codes
	KeyOffset  = "tg_offset" // the last update taken, so a restart does not hand them out again
)

// Enabled switches the bot on; off until the admin connects it.
var Enabled = settings.Switch{Key: KeyEnabled}

// MaxLinks: subscriptions one Telegram account may hold.
const MaxLinks = 5

// Deps is what the bot needs from the panel.
type Deps struct {
	Store    *store.Store
	Settings *settings.Settings
	Devices  *domain.Devices
	// SubBase is https://host:port/<sub path>, "" while the panel has no public address.
	SubBase func(ctx context.Context) string
	// MiniApp: the panel's certificate is one Telegram apps accept (not self-signed).
	MiniApp func() bool
	API     string // the Bot API; DefaultAPI unless testing
	Log     *slog.Logger
	Now     func() time.Time
	Limits  Limits // zero: DefaultLimits
	// Billing sells tariffs in the menu; nil: no shop.
	Billing *billing.Service
	// Tunnel reaches addr through node id, for RouteNode; nil: the panel has no nodes.
	Tunnel func(ctx context.Context, nodeID int64, addr string) (net.Conn, error)

	// stars takes a successful Stars payment instead of Billing; tests only.
	stars func(ctx context.Context, tgID int64, payload, chargeID, currency string, amount int64) error
}

// Status is what the admin panel shows.
type Status struct {
	Running bool
	Error   string
	Bot     User
}

type Bot struct {
	d      Deps
	reload chan struct{}
	out    atomic.Pointer[Outbox] // nil while the bot is off
	client atomic.Pointer[Client] // the running bot's, for calls outside the update loop

	mu     sync.Mutex
	status Status
	input  map[int64][]time.Time // chat → its recent messages and presses, for the flood guard
	bcast  BroadcastProgress
	// Subscriptions somebody asked to take over: by subscription, and who was refused.
	transfers map[int64]transfer
	refused   map[string]time.Time

	noticeMu sync.Mutex
	notices  map[string]*noticeState

	// What the bot started and has not ended: the poll loop and its outbox, and the calls
	// that must not hold the loop up. Run returns when they have all returned, so that the
	// database is not closed under them.
	running sync.WaitGroup
}

// BroadcastProgress is the last broadcast as the admin panel shows it.
type BroadcastProgress struct {
	Total, Sent, Failed int
	Started             time.Time
}

// Active: some of it is still being sent.
func (p BroadcastProgress) Active() bool { return p.Sent+p.Failed < p.Total }

func New(d Deps) *Bot {
	if d.API == "" {
		d.API = DefaultAPI
	}
	if d.Limits == (Limits{}) {
		d.Limits = DefaultLimits
	}
	return &Bot{d: d, reload: make(chan struct{}, 1), input: map[int64][]time.Time{},
		transfers: map[int64]transfer{}, refused: map[string]time.Time{}, notices: map[string]*noticeState{}}
}

// Status of the running bot.
func (b *Bot) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status
}

func (b *Bot) setStatus(fn func(*Status)) {
	b.mu.Lock()
	fn(&b.status)
	b.mu.Unlock()
}

// Reload restarts the bot with the settings as they are now.
func (b *Bot) Reload() {
	select {
	case b.reload <- struct{}{}:
	default:
	}
}

// Run keeps the bot polling while it is on, and sends the notifications.
func (b *Bot) Run(ctx context.Context) {
	notify := time.NewTicker(10 * time.Minute)
	defer notify.Stop()
	var stop context.CancelFunc
	start := func() {
		if stop != nil {
			stop()
			stop = nil
		}
		on, _ := b.d.Settings.On(ctx, Enabled)
		token, _ := b.d.Settings.String(ctx, KeyToken)
		if !on || token == "" {
			b.setStatus(func(s *Status) { *s = Status{} })
			return
		}
		rt, err := b.transport(b.Route(ctx))
		if err != nil {
			b.setStatus(func(s *Status) { *s = Status{Error: "route_invalid"} })
			return
		}
		pctx, cancel := context.WithCancel(ctx)
		stop = cancel
		c := NewClient(b.d.API, token, rt)
		b.running.Go(func() { b.poll(pctx, c) })
	}
	start()
	for {
		select {
		case <-ctx.Done():
			if stop != nil {
				stop()
			}
			b.running.Wait()
			return
		case <-b.reload:
			start()
		case <-notify.C:
			b.Notify(ctx)
		}
	}
}

// Config is the admin's setup, or the default one in the panel's default language.
func (b *Bot) Config(ctx context.Context) Config {
	lang, _ := b.d.Settings.Lang(ctx)
	// A setup saved before an option existed gets that option's default. A saved menu
	// replaces the default one whole.
	def := Default(lang)
	def.Buttons = nil
	cfg, ok, err := settings.GetOver(ctx, b.d.Settings, KeyConfig, def)
	if err != nil || !ok {
		return Default(lang)
	}
	_ = cfg.Validate()
	return cfg
}

// secret signs link codes; made once per panel.
func (b *Bot) secret(ctx context.Context) ([]byte, error) {
	s, err := b.d.Settings.String(ctx, KeySecret)
	if err != nil {
		return nil, err
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil && len(raw) == 32 {
		return raw, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	return raw, settings.Set(ctx, b.d.Settings, KeySecret, base64.StdEncoding.EncodeToString(raw))
}

// LinkURL is the t.me link that opens the bot with a code for the user's subscription;
// "" while the bot is off.
func (b *Bot) LinkURL(ctx context.Context, userID int64) string {
	st := b.Status()
	if !st.Running || st.Bot.Username == "" {
		return ""
	}
	secret, err := b.secret(ctx)
	if err != nil {
		return ""
	}
	return "https://t.me/" + st.Bot.Username + "?start=" + LinkCode(secret, userID, b.d.Now())
}

func (b *Bot) poll(ctx context.Context, c *Client) {
	me, err := c.Me(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return // reloaded meanwhile: the new run's status is not ours to overwrite
		}
		b.setStatus(func(s *Status) { *s = Status{Error: errText(err)} })
		b.retryLater(ctx)
		return
	}
	_ = settings.Set(ctx, b.d.Settings, KeyBot, me)
	_ = c.DropWebhook(ctx)
	cfg := b.Config(ctx)
	w := wordsFor(cfg.Lang)
	_ = c.SetCommands(ctx, w.commands)
	if url := b.miniAppURL(ctx, cfg); url != "" {
		_ = c.SetMenuButton(ctx, w.menuApp, url)
	} else {
		_ = c.SetMenuButton(ctx, "", "")
	}
	// Every message the bot sends goes through the outbox (see outbox.go).
	out := NewOutbox(c, b.d.Limits, time.Now, func(chat int64) {
		_ = b.d.Store.Q.SetTgBlocked(context.WithoutCancel(ctx), db.SetTgBlockedParams{Blocked: 1, TgID: chat})
	}, func(err error) { b.d.Log.Warn("telegram: send", "err", errText(err)) })
	b.running.Go(func() { out.Run(ctx) })
	b.out.Store(out)
	b.client.Store(c)
	defer b.client.CompareAndSwap(c, nil)
	defer b.out.CompareAndSwap(out, nil)
	b.setStatus(func(s *Status) { *s = Status{Running: true, Bot: me} })
	offset := b.loadOffset(ctx, me.ID)
	fails := map[int64]int{} // update → times it failed to be handled
	backoff := time.Second
	for ctx.Err() == nil {
		ups, err := c.Updates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var ae *APIError
			if errors.As(err, &ae) && ae.Code == 401 {
				b.setStatus(func(s *Status) { *s = Status{Error: "token_revoked"} })
				return
			}
			b.setStatus(func(s *Status) { s.Error = errText(err) })
			sleep(ctx, backoff)
			backoff = min(2*backoff, time.Minute)
			continue
		}
		backoff = time.Second
		b.setStatus(func(s *Status) { s.Error = "" })
		for _, up := range ups {
			if err := b.handle(ctx, c, up); err != nil && ctx.Err() == nil {
				// Not acknowledged: Telegram sends it again, with whatever came after.
				if fails[up.UpdateID]++; fails[up.UpdateID] < maxUpdateTries {
					sleep(ctx, time.Duration(fails[up.UpdateID])*updateRetryPause)
					break
				}
				b.d.Log.Error("telegram: update given up", "update", up.UpdateID, "err", err)
			}
			if ctx.Err() != nil {
				return // not handled to the end: stays unacknowledged for the next run
			}
			delete(fails, up.UpdateID)
			offset = up.UpdateID + 1
			b.saveOffset(ctx, me.ID, offset)
		}
	}
}

// maxUpdateTries: how often an update that cannot be handled is offered again before it
// is dropped, so one that never works does not stop the bot. updateRetryPause is the wait
// before each new try, longer every time.
const maxUpdateTries = 5

var updateRetryPause = 2 * time.Second

// offsetRecord is the saved position in Telegram's update stream, of one bot.
type offsetRecord struct {
	Bot    int64 `json:"bot"`
	Offset int64 `json:"offset"`
}

// loadOffset: where this bot's updates continue. What Telegram keeps for a bot that was
// switched off or restarted is handed out again from the last confirmation, which is the
// next getUpdates; saving the offset keeps a restart from replaying the last batch (taps
// that unbind a device, a purchase's confirmation) a second time.
func (b *Bot) loadOffset(ctx context.Context, bot int64) int64 {
	rec, ok, err := settings.Get[offsetRecord](ctx, b.d.Settings, KeyOffset)
	if err != nil || !ok || rec.Bot != bot {
		return 0
	}
	return rec.Offset
}

// saveOffset writes the row itself, not through settings.Set: the position moves with
// every update, and it is not a setting anything is built from (settings.Generation).
func (b *Bot) saveOffset(ctx context.Context, bot, offset int64) {
	raw, _ := json.Marshal(offsetRecord{Bot: bot, Offset: offset})
	if err := b.d.Store.Q.SetSetting(ctx, db.SetSettingParams{Key: KeyOffset, Value: string(raw)}); err != nil && ctx.Err() == nil {
		b.d.Log.Warn("telegram: offset not saved", "err", err)
	}
}

// retryLater re-reads the settings in a minute: a network hiccup at start must not leave
// the bot off until the admin touches it.
func (b *Bot) retryLater(ctx context.Context) {
	b.running.Go(func() {
		sleep(ctx, time.Minute)
		if ctx.Err() == nil {
			b.Reload()
		}
	})
}

func errText(err error) string {
	var ae *APIError
	switch {
	case errors.As(err, &ae) && ae.Code == 401:
		return "token_invalid"
	case errors.As(err, &ae):
		return ae.Description
	}
	return "unreachable"
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// A person taps and types far slower than this; past it a chat is ignored for a while,
// so one abuser cannot spend the bot's Telegram limits.
const (
	floodWindow = 10 * time.Second
	floodInputs = 20
)

func (b *Bot) flooding(chat int64) bool {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	recent := b.input[chat][:0]
	for _, t := range b.input[chat] {
		if now.Sub(t) < floodWindow {
			recent = append(recent, t)
		}
	}
	b.input[chat] = append(recent, now)
	if len(b.input) > 10000 {
		for k, ts := range b.input {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > floodWindow {
				delete(b.input, k)
			}
		}
	}
	return len(recent) >= floodInputs
}

// handle takes one update. Only a payment can fail in a way that is worth offering the
// update again for; the rest is answered, or dropped, as it comes.
func (b *Bot) handle(ctx context.Context, c *Client, up Update) error {
	out := b.out.Load()
	if out == nil {
		return nil
	}
	switch {
	case up.PreCheckoutQuery != nil:
		// Ten seconds from Telegram, whatever else the loop waits for: its own goroutine.
		b.running.Go(func() { b.preCheckout(ctx, c, up.PreCheckoutQuery) })
	case up.Message != nil && up.Message.SuccessfulPayment != nil && up.Message.Chat.Type == "private":
		return b.starsPaid(ctx, up.Message)
	case up.CallbackQuery != nil && up.CallbackQuery.Message != nil && up.CallbackQuery.Message.Chat.Type == "private":
		if cmd, _, _ := strings.Cut(up.CallbackQuery.Data, ":"); cmd == "ta" || cmd == "tx" {
			b.onTransfer(ctx, c, out, up.CallbackQuery)
		} else {
			b.onPress(ctx, c, out, up.CallbackQuery)
		}
	case up.Message != nil && up.Message.From != nil && up.Message.Chat.Type == "private":
		b.onMessage(ctx, out, up.Message)
	}
	return nil
}

var subLink = regexp.MustCompile(`https?://\S+/([A-Za-z0-9]{24})(?:[/?#]\S*)?`)

// onMessage: /start (with a code from a subscription page), a subscription link, or
// anything else — every message brings the main menu back to the bottom of the chat.
func (b *Bot) onMessage(ctx context.Context, out *Outbox, m *Message) {
	chat, now := m.Chat.ID, b.d.Now().Unix()
	if b.flooding(chat) {
		return
	}
	_ = b.d.Store.Q.UpsertTgChat(ctx, db.UpsertTgChatParams{TgID: chat, Username: m.From.Username, FirstName: m.From.FirstName, CreatedAt: now, UpdatedAt: now})
	cfg := b.Config(ctx)
	w := wordsFor(cfg.Lang)
	if cfg.CleanChat {
		id := m.MessageID
		out.Reply(chat, "", 1, func(ctx context.Context, c *Client) error { return c.Delete(ctx, chat, id) })
	}
	var notice string
	text := strings.TrimSpace(m.Text)
	switch {
	case strings.HasPrefix(text, "/start "):
		notice = b.linkByCode(ctx, out, w, chat, who(m.From), strings.TrimSpace(strings.TrimPrefix(text, "/start ")))
	case subLink.MatchString(text):
		notice = b.linkByToken(ctx, out, w, chat, who(m.From), subLink.FindStringSubmatch(text)[1])
	}
	b.freshMenu(out, chat, notice)
}

// freshMenu sends the main menu as a new message and removes the previous one, so the
// chat keeps a single menu at the bottom. Several messages in a row get one new menu.
func (b *Bot) freshMenu(out *Outbox, chat int64, notice string) {
	out.Reply(chat, "menu:"+strconv.FormatInt(chat, 10), 2, func(ctx context.Context, c *Client) error {
		return b.sendMenu(ctx, c, chat, notice)
	})
}

func (b *Bot) sendMenu(ctx context.Context, c *Client, chat int64, notice string) error {
	text, kb := b.screen(ctx, b.Config(ctx), chat, "m", notice)
	sent, err := c.Send(ctx, chat, text, kb, false)
	if err != nil {
		return err
	}
	if old, err := b.d.Store.Q.GetTgChat(ctx, chat); err == nil && old.MenuMsgID != 0 && old.MenuMsgID != sent.MessageID {
		_ = c.Delete(ctx, chat, old.MenuMsgID)
	}
	_ = b.d.Store.Q.SetTgMenu(ctx, db.SetTgMenuParams{MenuMsgID: sent.MessageID, TgID: chat})
	return nil
}

// answer stops the spinner on a pressed button. A call to Telegram that hangs must not
// hold up the update loop, so it goes on its own.
func (b *Bot) answer(ctx context.Context, c *Client, id string) {
	b.running.Go(func() { _ = c.Answer(ctx, id, "") })
}

// onPress edits the menu message in place: the chat never fills up with menus. The
// spinner on the button stops at once; the screen is drawn when the chat's turn comes,
// and a newer tap on the same menu replaces a screen not yet drawn.
func (b *Bot) onPress(ctx context.Context, c *Client, out *Outbox, q *CallbackQuery) {
	chat, msg := q.Message.Chat.ID, q.Message.MessageID
	if b.flooding(chat) {
		return
	}
	b.answer(ctx, c, q.ID)
	// What a tap changes happens now, only its screen may wait.
	data, notice := b.act(ctx, chat, q.Data)
	out.Reply(chat, "edit:"+strconv.FormatInt(chat, 10)+":"+strconv.FormatInt(msg, 10), 1, func(ctx context.Context, c *Client) error {
		text, kb := b.screen(ctx, b.Config(ctx), chat, data, notice)
		if err := c.Edit(ctx, chat, msg, text, kb); err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Code == 429 {
				return err
			}
			// Too old to edit, or gone: a new menu instead.
			return b.sendMenu(ctx, c, chat, notice)
		}
		_ = b.d.Store.Q.SetTgMenu(ctx, db.SetTgMenuParams{MenuMsgID: msg, TgID: chat})
		return nil
	})
}

func (b *Bot) linkByCode(ctx context.Context, out *Outbox, w *words, chat int64, name, code string) string {
	secret, err := b.secret(ctx)
	if err != nil {
		return w.linkInvalid
	}
	id, err := ParseLinkCode(secret, code, b.d.Now())
	switch {
	case errors.Is(err, ErrLinkExpired):
		return w.linkExpired
	case err != nil:
		return w.linkInvalid
	}
	return b.link(ctx, out, w, chat, name, id)
}

func (b *Bot) linkByToken(ctx context.Context, out *Outbox, w *words, chat int64, name, token string) string {
	u, err := b.d.Store.Q.GetUserBySubToken(ctx, token)
	if err != nil {
		return w.linkInvalid
	}
	return b.link(ctx, out, w, chat, name, u.ID)
}

// link ties a subscription to the chat's account. A subscription has one owner. The link
// to it is no proof of being the owner (it gets forwarded, shown on screenshots), so one
// that is already linked is moved only with its owner's yes, asked in the owner's own chat;
// an owner who cannot be asked, as they blocked the bot, is not in the way.
func (b *Bot) link(ctx context.Context, out *Outbox, w *words, chat int64, name string, userID int64) string {
	u, err := b.d.Store.Q.GetUser(ctx, userID)
	if err != nil {
		return w.linkInvalid
	}
	prev, err := b.d.Store.Q.GetTgLink(ctx, userID)
	switch {
	case err == nil && prev.TgID == chat:
		_ = b.d.Store.Q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: userID, TgID: chat})
		return fmt.Sprintf(w.alreadyLinked, u.Name)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return w.linkInvalid
	}
	if n, err := b.d.Store.Q.CountTgLinksOf(ctx, chat); err != nil || n >= MaxLinks {
		return fmt.Sprintf(w.linkLimit, MaxLinks)
	}
	if prev.TgID != 0 && b.reachable(ctx, prev.TgID) {
		return b.askOwner(ctx, out, w, chat, name, u, prev.TgID)
	}
	if err := b.d.Store.Q.LinkTg(ctx, db.LinkTgParams{UserID: userID, TgID: chat, CreatedAt: b.d.Now().Unix()}); err != nil {
		return w.linkInvalid
	}
	_ = b.d.Store.Q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: userID, TgID: chat})
	return fmt.Sprintf(w.linked, u.Name)
}

// reachable: the account has a chat with the bot and has not blocked it, so it can be asked.
func (b *Bot) reachable(ctx context.Context, tgID int64) bool {
	ch, err := b.d.Store.Q.GetTgChat(ctx, tgID)
	return err == nil && ch.Blocked == 0
}

// who is how a Telegram account is named to the owner of a subscription it asks for.
func who(u *User) string {
	switch {
	case u == nil:
		return "?"
	case u.Username != "":
		return "@" + u.Username
	case u.FirstName != "":
		return u.FirstName
	}
	return strconv.FormatInt(u.ID, 10)
}

// transfer is another account's request for a subscription, open until the owner answers
// or the time is up.
type transfer struct {
	to    int64
	until time.Time
}

const (
	transferTTL    = time.Hour
	transferRefuse = 24 * time.Hour // after a refusal, the same account does not ask again for this long
)

func refusedKey(userID, chat int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(chat, 10)
}

// askOwner sends the subscription's owner the question and tells the one who asked to wait.
func (b *Bot) askOwner(ctx context.Context, out *Outbox, w *words, chat int64, name string, u db.User, owner int64) string {
	now := b.d.Now()
	b.mu.Lock()
	t, open := b.transfers[u.ID]
	if open && now.After(t.until) {
		delete(b.transfers, u.ID)
		open = false
	}
	switch {
	case open && t.to == chat:
		b.mu.Unlock()
		return w.transferAsked
	case open:
		b.mu.Unlock()
		return w.transferBusy
	case now.Before(b.refused[refusedKey(u.ID, chat)]):
		b.mu.Unlock()
		return w.transferDeniedNew
	}
	b.transfers[u.ID] = transfer{to: chat, until: now.Add(transferTTL)}
	b.mu.Unlock()
	text := html.EscapeString(fmt.Sprintf(w.transferAsk, u.Name, name))
	data := strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(chat, 10)
	kb := &Keyboard{[][]Button{{{Text: w.transferAllow, CallbackData: "ta:" + data}, {Text: w.transferDeny, CallbackData: "tx:" + data}}}}
	out.Notice(owner, func(ctx context.Context, c *Client) error {
		_, err := c.Send(ctx, owner, text, kb, false)
		return err
	}, func(err error) {
		if err != nil { // the owner was not reached: the request lapses, and can be made again
			b.mu.Lock()
			delete(b.transfers, u.ID)
			b.mu.Unlock()
		}
	})
	return w.transferAsked
}

// onTransfer is the owner's answer to askOwner. It counts only from the owner's own chat,
// for the very request that is open, and while it is.
func (b *Bot) onTransfer(ctx context.Context, c *Client, out *Outbox, q *CallbackQuery) {
	chat, msg := q.Message.Chat.ID, q.Message.MessageID
	if b.flooding(chat) {
		return
	}
	b.answer(ctx, c, q.ID)
	cmd, arg, _ := strings.Cut(q.Data, ":")
	uidStr, toStr, _ := strings.Cut(arg, ":")
	userID, _ := strconv.ParseInt(uidStr, 10, 64)
	to, _ := strconv.ParseInt(toStr, 10, 64)
	w := wordsFor(b.lang(ctx))
	say := func(text string) { // the question's message becomes the answer, without buttons
		out.Reply(chat, "edit:"+strconv.FormatInt(chat, 10)+":"+strconv.FormatInt(msg, 10), 1, func(ctx context.Context, c *Client) error {
			return c.Edit(ctx, chat, msg, html.EscapeString(text), nil)
		})
	}
	tell := func(to int64, text string) {
		out.Notice(to, func(ctx context.Context, c *Client) error {
			_, err := c.Send(ctx, to, html.EscapeString(text), nil, false)
			return err
		}, nil)
	}
	now := b.d.Now()
	b.mu.Lock()
	t, open := b.transfers[userID]
	link, err := b.d.Store.Q.GetTgLink(ctx, userID)
	if !open || t.to != to || now.After(t.until) || err != nil || link.TgID != chat {
		b.mu.Unlock()
		say(w.transferStale)
		return
	}
	delete(b.transfers, userID)
	if cmd == "tx" {
		b.refused[refusedKey(userID, to)] = now.Add(transferRefuse)
		if len(b.refused) > 1000 {
			for k, until := range b.refused {
				if now.After(until) {
					delete(b.refused, k)
				}
			}
		}
	}
	b.mu.Unlock()
	u, err := b.d.Store.Q.GetUser(ctx, userID)
	if err != nil {
		say(w.transferStale)
		return
	}
	if cmd == "tx" {
		say(fmt.Sprintf(w.transferDenied, u.Name))
		tell(to, w.transferDeniedNew)
		return
	}
	if n, err := b.d.Store.Q.CountTgLinksOf(ctx, to); err != nil || n >= MaxLinks {
		say(w.transferStale)
		tell(to, fmt.Sprintf(w.linkLimit, MaxLinks))
		return
	}
	if err := b.d.Store.Q.LinkTg(ctx, db.LinkTgParams{UserID: userID, TgID: to, CreatedAt: now.Unix()}); err != nil {
		say(w.transferStale)
		return
	}
	_ = b.d.Store.Q.SetTgCurrent(ctx, db.SetTgCurrentParams{Current: userID, TgID: to})
	say(fmt.Sprintf(w.transferDone, u.Name))
	b.freshMenu(out, to, fmt.Sprintf(w.transferNotice, u.Name))
}

// subs are the chat's subscriptions and the one it shows.
func (b *Bot) subs(ctx context.Context, chat int64) ([]db.User, db.User, bool) {
	list, err := b.d.Store.Q.ListTgLinksOf(ctx, chat)
	if err != nil || len(list) == 0 {
		return nil, db.User{}, false
	}
	cur := list[0]
	if ch, err := b.d.Store.Q.GetTgChat(ctx, chat); err == nil {
		for _, u := range list {
			if u.ID == ch.Current {
				cur = u
			}
		}
	}
	return list, cur, true
}

func (b *Bot) miniAppURL(ctx context.Context, cfg Config) string {
	base := b.d.SubBase(ctx)
	if !cfg.MiniApp || base == "" || b.d.MiniApp == nil || !b.d.MiniApp() {
		return ""
	}
	return base + "/tg"
}

func labelOf(cfg Config, action, def string) string {
	for _, btn := range cfg.Buttons {
		if btn.Action == action {
			return btn.Label
		}
	}
	return def
}

// CheckToken asks Telegram whose token this is, the way the bot goes there.
func (b *Bot) CheckToken(ctx context.Context, token string) (User, error) {
	return b.CheckTokenVia(ctx, token, b.Route(ctx))
}

// CheckTokenVia asks Telegram whose token this is, through route: a route and a token
// changed together are checked as they will run.
func (b *Bot) CheckTokenVia(ctx context.Context, token string, route Route) (User, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rt, err := b.transport(route)
	if err != nil {
		return User{}, err
	}
	return NewClient(b.d.API, token, rt).Me(ctx)
}

// MiniAppURL is where the bot's Mini App opens; "" when Telegram could not load it.
func (b *Bot) MiniAppURL(ctx context.Context) string { return b.miniAppURL(ctx, b.Config(ctx)) }
