package node

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mikan/internal/nodeapi"
	"mikan/internal/nodetls"
)

// The panel reaches Telegram through a node: the node opens a tunnel to a tunnel host
// and to nothing else, on its unix socket and over the panel's mTLS alike.
func TestConnectTunnel(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "bot api")
	}))
	t.Cleanup(target.Close)
	addr := target.Listener.Addr().String()
	saved := nodeapi.TunnelHosts
	nodeapi.TunnelHosts = []string{addr}
	t.Cleanup(func() { nodeapi.TunnelHosts = saved })

	serve := func(ln net.Listener) {
		srv := &http.Server{Handler: Handler(nil, slog.New(slog.NewTextHandler(io.Discard, nil))), ReadHeaderTimeout: time.Second}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
	}
	sock := filepath.Join(t.TempDir(), "node.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	serve(ln)
	clients := map[string]*nodeapi.Client{"unix": nodeapi.NewUnixClient(sock), "mtls": mtlsNode(t, serve)}

	for name, c := range clients {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, err := c.Tunnel(ctx, addr)
		if err != nil {
			cancel()
			t.Fatalf("%s: %v", name, err)
		}
		// Past the read header timeout: the tunnel keeps no deadline of the Node API.
		time.Sleep(1200 * time.Millisecond)
		if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: "+addr+"\r\nConnection: close\r\n\r\n"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		conn.Close()
		if resp.StatusCode != http.StatusOK || string(body) != "bot api" {
			t.Fatalf("%s through the tunnel: %d %q", name, resp.StatusCode, body)
		}
		for _, host := range []string{"example.com:443", "127.0.0.1:22", strings.Replace(addr, "127.0.0.1", "localhost", 1)} {
			_, err := c.Tunnel(ctx, host)
			var ne *nodeapi.Error
			if !errors.As(err, &ne) || ne.Code != "tunnel_forbidden" {
				t.Errorf("%s tunnel to %s: %v", name, host, err)
			}
		}
		cancel()
	}
}

// mtlsNode serves the Node API over TLS the way a remote node does and returns the
// panel's client for it.
func mtlsNode(t *testing.T, serve func(net.Listener)) *nodeapi.Client {
	t.Helper()
	now := time.Now()
	panel, err := nodetls.Generate("mikan-panel", x509.ExtKeyUsageClientAuth, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := nodetls.Generate("node-2.mikan", x509.ExtKeyUsageServerAuth, now)
	if err != nil {
		t.Fatal(err)
	}
	panelPin, _ := nodetls.Fingerprint(panel.CertPEM)
	nodePin, _ := nodetls.Fingerprint(node.CertPEM)
	scfg, err := nodetls.Key{Port: 1, PanelPin: panelPin, CertPEM: node.CertPEM, KeyPEM: node.KeyPEM}.ServerConfig()
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serve(tls.NewListener(tcp, scfg))
	ccfg, err := nodetls.ClientConfig(panel, nodePin)
	if err != nil {
		t.Fatal(err)
	}
	return nodeapi.NewTLSClient(tcp.Addr().String(), ccfg)
}
