package tgbot

import (
	"testing"

	"mikan/internal/panel/billing"
)

// Marketplace adapters get a button each, by their own name, and their code names them
// back; a code that is not an adapter id names nothing.
func TestAddonButtons(t *testing.T) {
	// YooKassa and CryptoBot keep the words and short codes of the built-in providers they
	// replaced, and come first; the others go by their own names.
	av := billing.Available{Stars: true, Addons: []string{"cryptobot", "pay-2", "yookassa"}}
	rows := ru.payButtons("py:7:", 150, 19900, av, func(id string) string { return "Name of " + id })
	want := [][2]string{{"⭐ Telegram Stars — ⭐ 150", "py:7:s"}, {"💳 Картой или СБП — 199 ₽", "py:7:y"}, {"🪙 Криптовалютой — 199 ₽", "py:7:c"},
		{"💳 Name of pay-2 — 199 ₽", "py:7:a-pay-2"}}
	if len(rows) != len(want) {
		t.Fatalf("rows: %+v", rows)
	}
	for i, w := range want {
		if b := rows[i][0]; b.Text != w[0] || b.CallbackData != w[1] || len(b.CallbackData) > 64 {
			t.Errorf("row %d: %q %q, want %q %q", i, b.Text, b.CallbackData, w[0], w[1])
		}
	}
	if rows := ru.payButtons("py:7:", 150, 0, av, func(string) string { return "" }); len(rows) != 1 {
		t.Fatalf("an adapter offered without a ruble price: %+v", rows)
	}
	for code, want := range map[string]string{"s": billing.Stars, "y": "addon:yookassa", "c": "addon:cryptobot", "a-yookassa": "addon:yookassa", "a-pay-2": "addon:pay-2", "a-": "", "a-../x": "", "a-Big": "", "x": ""} {
		if got, ok := providerOf(code); got != want || ok != (want != "") {
			t.Errorf("providerOf(%q) = %q %v, want %q", code, got, ok, want)
		}
	}
}
