package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mikan/internal/panel/audit"
	"mikan/internal/panel/settings"
	"mikan/internal/panel/store"
	"mikan/internal/panel/tlscert"
)

// certCmd installs the admin's own certificate from the server (GitHub issue #9): the
// host's `mikan cert set` pipes the chain and the key in, which suits a certbot or
// acme.sh renewal hook. The running panel is told to serve it at once.
func certCmd(ctx context.Context, st *store.Store, set *settings.Settings, dataDir string, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("mikan admin cert set|clear|show [--node NODE]; set reads the chain and the key (PEM) on stdin")
	}
	fs := flag.NewFlagSet("cert "+args[0], flag.ContinueOnError)
	node := fs.Int64("node", 0, "a node's own certificate instead of the panel's (see mikan admin node list)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	now := time.Now()
	panelDir := filepath.Join(dataDir, "tls", "custom")
	nodes := tlscert.NewNodeStore(filepath.Join(dataDir, "tls", "custom-nodes"), time.Now)
	if *node != 0 {
		if _, err := st.Q.GetNode(ctx, *node); err != nil {
			return fmt.Errorf("no node %d: see mikan admin node list", *node)
		}
	}
	target := "the panel"
	if *node != 0 {
		target = fmt.Sprintf("node %d", *node)
	}
	switch args[0] {
	case "set":
		// One stream with both: the certificate blocks and the key block are told apart.
		raw, err := io.ReadAll(io.LimitReader(stdin, 2*tlscert.MaxPEM))
		if err != nil {
			return err
		}
		if *node != 0 {
			cert, err := nodes.Set(*node, raw, raw)
			if err != nil {
				return certCLIError(err, "")
			}
			describe(stdout, target, tlscert.Describe(cert, "", now))
			fmt.Fprintln(stdout, "The node gets it with its next sync, within a minute.")
		} else {
			cert, err := tlscert.ParseCustom(raw, raw, now)
			if err != nil {
				return certCLIError(err, "")
			}
			host, err := panelHost(ctx, set)
			if err != nil {
				return err
			}
			if host != "" && !tlscert.Covers(cert.Leaf, host) {
				return certCLIError(tlscert.ErrWrongHost, host)
			}
			if err := tlscert.SaveCustom(panelDir, cert); err != nil {
				return err
			}
			describe(stdout, target, tlscert.Describe(cert, host, now))
			reloadPanel(stdout)
		}
		_ = audit.Write(ctx, st.Q, now, audit.Entry{Action: "cli.certificate", TargetType: "certificate", TargetID: target, Details: map[string]any{"kind": "custom"}})
		return nil
	case "clear":
		var err error
		if *node != 0 {
			err = nodes.Clear(*node)
		} else {
			err = tlscert.RemoveCustom(panelDir)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed the own certificate of %s.\n", target)
		if *node == 0 {
			reloadPanel(stdout)
		}
		_ = audit.Write(ctx, st.Q, now, audit.Entry{Action: "cli.certificate", TargetType: "certificate", TargetID: target, Details: map[string]any{"kind": "automatic"}})
		return nil
	case "show":
		var (
			cert *tlscert.Info
			err  error
		)
		if *node != 0 {
			c, _, e := nodes.Get(*node, "")
			if c != nil {
				i := tlscert.Describe(c, "", now)
				cert = &i
			}
			err = e
		} else if tlscert.HasCustom(panelDir) {
			c, e := tlscert.LoadCustom(panelDir, now)
			if e == nil {
				host, _ := panelHost(ctx, set)
				i := tlscert.Describe(c, host, now)
				cert = &i
			}
			err = e
		}
		switch {
		case err != nil:
			fmt.Fprintf(stdout, "%s has an own certificate that is not used: %v\n", target, err)
		case cert == nil:
			fmt.Fprintf(stdout, "%s has no own certificate.\n", target)
		default:
			describe(stdout, target, *cert)
		}
		return nil
	}
	return fmt.Errorf("unknown cert command %q: set, clear or show", args[0])
}

func panelHost(ctx context.Context, set *settings.Settings) (string, error) {
	d, err := set.String(ctx, settings.KeyDomain)
	if err != nil || d != "" {
		return d, err
	}
	return set.String(ctx, settings.KeyPublicHost)
}

func describe(w io.Writer, target string, i tlscert.Info) {
	trust := "not publicly trusted: clients pin it"
	if i.Trusted {
		trust = "publicly trusted"
	}
	fmt.Fprintf(w, "Own certificate of %s: %s, by %s, until %s (%s).\n", target, strings.Join(i.Names, ", "), i.Issuer, i.NotAfter.Format("2006-01-02"), trust)
}

// reloadPanel asks the running panel (process 1 of the container) to serve the files
// now; without it the panel notices them within half a minute anyway.
func reloadPanel(w io.Writer) {
	if p, err := os.FindProcess(1); err == nil && p.Signal(syscall.SIGHUP) == nil {
		fmt.Fprintln(w, "The panel serves it now.")
		return
	}
	fmt.Fprintln(w, "The panel serves it within half a minute.")
}

var certHints = map[error]string{
	tlscert.ErrCertPEM:     "no certificate in PEM: pass fullchain.pem",
	tlscert.ErrKeyPEM:      "no private key in PEM: pass privkey.pem",
	tlscert.ErrKeyMismatch: "the key is not the certificate's",
	tlscert.ErrKeyWeak:     "the key is too weak: RSA of 2048 bits or more, ECDSA P-256/384/521 or Ed25519",
	tlscert.ErrExpired:     "the certificate has expired",
	tlscert.ErrNotYet:      "the certificate is not valid yet",
}

func certCLIError(err error, host string) error {
	if errors.Is(err, tlscert.ErrWrongHost) {
		return fmt.Errorf("the certificate is not for %s, the panel's address: clients would refuse it", host)
	}
	for e, hint := range certHints {
		if errors.Is(err, e) {
			return errors.New(hint)
		}
	}
	return err
}
