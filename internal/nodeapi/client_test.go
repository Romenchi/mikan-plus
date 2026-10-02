package nodeapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// endless writes a JSON object that never ends.
func endless(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"epoch":"e","seq":1,"slots":{`)
	chunk := strings.Repeat(`"s000001":{"up":1,"down":1},`, 4096)
	for {
		if _, err := io.WriteString(w, chunk); err != nil {
			return
		}
	}
}

// A node is a server somebody else may run: an answer that never ends must be refused
// at a size, not buffered until the panel runs out of memory.
func TestAnswerSizeIsCapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(endless))
	defer srv.Close()
	c := &Client{hc: srv.Client(), base: srv.URL}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := c.Counters(context.Background())
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an endless answer: %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8*MaxResponse {
		t.Fatalf("the panel allocated %d MiB for one answer", grew>>20)
	}
}

func TestAnswerWithinTheCapIsRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"epoch":"e","seq":3,"slots":{"s1":{"up":1,"down":2}},"future_field":true}`)
	}))
	defer srv.Close()
	c := &Client{hc: srv.Client(), base: srv.URL}
	got, err := c.Counters(context.Background())
	if err != nil || got.Seq != 3 || got.Slots["s1"].Down != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}
