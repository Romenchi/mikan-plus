package tgbot

import (
	"context"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"

	"mikan/internal/panel/billing"
	"mikan/internal/panel/store/db"
)

// Traffic packages (GitHub issue #12) in the subscription screen. Callback data: x the
// packages for the shown subscription, xk:<package> one of them, xp:<package>:<p> pay
// for it (<p> as in the shop).

// packageOffers lists the packages the subscription can buy; nil without payments.
func (b *Bot) packageOffers(ctx context.Context, userID int64) []billing.PackageOffer {
	if b.d.Billing == nil {
		return nil
	}
	o, _, err := b.d.Billing.PackageOffers(ctx, userID)
	if err != nil {
		b.d.Log.Warn("telegram: package offers", "err", err)
		return nil
	}
	return o
}

// trafficShop draws the screens of buying traffic for subscription u.
func (b *Bot) trafficShop(ctx context.Context, w *words, chat int64, u db.User, cmd, arg, notice string) (string, *Keyboard) {
	switch cmd {
	case "xk":
		id, _ := strconv.ParseInt(arg, 10, 64)
		return b.trafficPackage(ctx, w, u.ID, id)
	case "xp":
		id, _, _ := strings.Cut(arg, ":")
		return b.trafficInvoice(ctx, w, chat, u.ID, arg, []Button{{Text: w.back, CallbackData: "xk:" + id}})
	}
	lines := []string{"<b>" + html.EscapeString(fmt.Sprintf(w.trafficTitle, u.Name)) + "</b>", ""}
	if notice != "" {
		lines = append([]string{html.EscapeString(notice), ""}, lines...)
	}
	lang := b.lang(ctx)
	rows := [][]Button{}
	offers := b.packageOffers(ctx, u.ID)
	for _, o := range offers {
		lines = append(lines, "• <b>"+html.EscapeString(o.Package.Name)+"</b> — "+html.EscapeString(billing.DescribePackage(o.Package, o.Pool, lang)))
		price := w.cheapest(billing.Offer{Stars: o.Stars, Rub: o.Rub})
		rows = append(rows, []Button{{Text: o.Package.Name + " · " + price, CallbackData: "xk:" + strconv.FormatInt(o.Package.ID, 10)}})
	}
	if len(offers) == 0 {
		lines = append(lines, html.EscapeString(w.payUnavailable))
	}
	return strings.Join(lines, "\n"), &Keyboard{append(rows, []Button{{Text: w.back, CallbackData: "s"}})}
}

// trafficPackage: one package and a button per way to pay.
func (b *Bot) trafficPackage(ctx context.Context, w *words, userID, id int64) (string, *Keyboard) {
	back := []Button{{Text: w.back, CallbackData: "x"}}
	if b.d.Billing == nil {
		return html.EscapeString(w.payUnavailable), &Keyboard{[][]Button{back}}
	}
	offers, av, err := b.d.Billing.PackageOffers(ctx, userID)
	if err != nil {
		return html.EscapeString(w.payUnavailable), &Keyboard{[][]Button{back}}
	}
	for _, o := range offers {
		if o.Package.ID != id {
			continue
		}
		text := "<b>" + html.EscapeString(o.Package.Name) + "</b>\n" + html.EscapeString(billing.DescribePackage(o.Package, o.Pool, b.lang(ctx))) + "\n\n" + w.payHow
		return text, &Keyboard{append(w.payButtons("xp:"+strconv.FormatInt(id, 10)+":", o.Stars, o.Rub, av, b.addonName(ctx)), back)}
	}
	return html.EscapeString(w.packageGone), &Keyboard{[][]Button{back}}
}

// payButtons: a button per provider that takes the price; data is the callback prefix
// the provider's code is added to, name names the marketplace's adapters.
func (w *words) payButtons(data string, stars, rub int64, av billing.Available, name func(id string) string) [][]Button {
	rows := [][]Button{}
	if stars > 0 {
		rows = append(rows, []Button{{Text: fmt.Sprintf(w.payStars, w.price(stars, "XTR")), CallbackData: data + "s"}})
	}
	if rub <= 0 {
		return rows
	}
	// Cards first: YooKassa, then CryptoBot, then the rest by id. Those two keep the words
	// buyers knew them by; the others go by their own names.
	ids := slices.Clone(av.Addons)
	rank := map[string]int{"yookassa": 0, "cryptobot": 1}
	slices.SortStableFunc(ids, func(a, b string) int {
		ra, oka := rank[a]
		rb, okb := rank[b]
		switch {
		case oka && okb:
			return ra - rb
		case oka:
			return -1
		case okb:
			return 1
		}
		return strings.Compare(a, b)
	})
	for _, id := range ids {
		text := fmt.Sprintf(w.payAddon, name(id), w.price(rub, "RUB"))
		switch id {
		case "yookassa":
			text = fmt.Sprintf(w.payCard, w.price(rub, "RUB"))
		case "cryptobot":
			text = fmt.Sprintf(w.payCrypto, w.price(rub, "RUB"))
		}
		rows = append(rows, []Button{{Text: text, CallbackData: data + addonCode(id)}})
	}
	return rows
}

// trafficInvoice opens the invoice for a package; like shopInvoice it runs off the update
// loop.
func (b *Bot) trafficInvoice(ctx context.Context, w *words, chat, userID int64, arg string, back []Button) (string, *Keyboard) {
	idStr, code, _ := strings.Cut(arg, ":")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	provider, ok := providerOf(code)
	if !ok || b.d.Billing == nil {
		return html.EscapeString(w.payUnavailable), &Keyboard{[][]Button{back}}
	}
	p, err := b.d.Billing.PackageInvoice(ctx, billing.PackageRequest{TgID: chat, UserID: userID, PackageID: id, Provider: provider})
	if err != nil {
		msg := w.payError(err)
		if msg == w.notForSale {
			msg = w.packageGone
		}
		return html.EscapeString(msg), &Keyboard{[][]Button{back}}
	}
	text := fmt.Sprintf(w.invoice, html.EscapeString(p.TariffName), html.EscapeString(w.price(p.Amount, p.Currency)), w.payPackage)
	return text, &Keyboard{[][]Button{{{Text: fmt.Sprintf(w.payButton, w.price(p.Amount, p.Currency)), URL: p.PayUrl}}, back}}
}

// withPackages is a quota with what is left of its packages: "100 GB + packages 32 GB".
func (w *words) withPackages(limit, extra int64) string {
	if extra <= 0 {
		return w.bytes(limit)
	}
	return fmt.Sprintf(w.plusPackages, w.bytes(limit), w.bytes(extra))
}
