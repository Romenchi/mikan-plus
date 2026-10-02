package app

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
)

// ErrSubPortBusy: something else on the server holds the port.
var ErrSubPortBusy = errors.New("sub_port_busy")

// SubPort serves subscriptions on a port of their own, beside the panel's, and moves at
// runtime when Settings change it. The panel's port keeps serving subscriptions too, so
// links already handed out keep working whatever the port becomes.
type SubPort struct {
	host string
	tls  *tls.Config // nil: plain HTTP (dev)
	log  *slog.Logger

	mu      sync.Mutex
	handler http.Handler
	port    int
	srv     *http.Server
	err     string
}

func NewSubPort(host string, tc *tls.Config, log *slog.Logger) *SubPort {
	return &SubPort{host: host, tls: tc, log: log}
}

// SetHandler is what the port serves: the subscription path alone.
func (s *SubPort) SetHandler(h http.Handler) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
}

// Set moves the listener to port; 0 closes it. The new port opens before the old one
// closes, so a port that cannot be had changes nothing.
func (s *SubPort) Set(port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if port == s.port && (port == 0 || s.srv != nil) {
		return nil
	}
	var srv *http.Server
	if port > 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort(s.host, strconv.Itoa(port)))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSubPortBusy, err)
		}
		if s.tls != nil {
			ln = tls.NewListener(ln, s.tls)
		}
		srv = httpServer(s.handler)
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.log.Error("subscription port", "port", port, "err", err)
			}
		}()
	}
	if s.srv != nil {
		_ = s.srv.Close()
	}
	s.srv, s.port, s.err = srv, port, ""
	return nil
}

// Start opens the saved port when the panel starts. A port taken meanwhile is reported
// in Settings, not fatal: the panel's own port still serves subscriptions.
func (s *SubPort) Start(port int) {
	if port <= 0 {
		return
	}
	if err := s.Set(port); err != nil {
		s.log.Warn("subscription port", "port", port, "err", err)
		s.mu.Lock()
		s.err = ErrSubPortBusy.Error()
		s.mu.Unlock()
	}
}

// Error is why the saved port is not served, or "".
func (s *SubPort) Error() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *SubPort) Close() {
	_ = s.Set(0)
}
