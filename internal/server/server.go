// Package server serves a pre-rendered dashboard over local HTTP and opens it in
// the browser, until the caller's context is cancelled — at which point it shuts
// down cleanly (AC-7). Serving over HTTP (rather than file://) sidesteps the
// same-origin restrictions a browser applies to local files; the HTML itself
// stays self-contained, so the on-disk copy remains double-clickable too.
//
// The package owns the lifecycle (listen, serve, open, graceful shutdown) but
// not the policy: the caller decides the address, supplies the HTML, and chooses
// when to stop by cancelling the context (e.g. a signal.NotifyContext on
// SIGINT). That seam keeps the serve CLI command (T6) thin and this package
// testable without sending real signals.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

// shutdownTimeout bounds how long a graceful shutdown waits for in-flight
// requests before giving up. The dashboard is a single static document, so this
// is generous.
const shutdownTimeout = 5 * time.Second

// BrowserOpener opens url in the user's default browser. It is injectable so
// tests can substitute a no-op and real runs use OpenBrowser.
type BrowserOpener func(url string) error

// Serve listens on addr, serves the dashboard HTML at "/", opens it in the
// browser, and blocks until ctx is cancelled, then shuts down gracefully and
// returns nil. A non-nil error means the server could not start or could not
// shut down cleanly; a failure to open the browser is reported via logf but is
// never fatal (the URL is still reachable manually).
//
// addr is any net.Listen TCP address, e.g. ":7777" or "127.0.0.1:0" (port 0
// picks a free port, used in tests). open and logf may be nil; defaults are
// OpenBrowser and a no-op.
func Serve(ctx context.Context, addr string, html []byte, open BrowserOpener, logf func(string, ...any)) error {
	if open == nil {
		open = OpenBrowser
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	})
	srv := &http.Server{Handler: mux}

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	url := browserURL(ln.Addr())
	logf("Serving llmlore dashboard at %s (press Ctrl+C to stop)", url)
	if err := open(url); err != nil {
		logf("Could not open the browser automatically; visit %s", url)
	}

	select {
	case err := <-serveErr:
		// Server stopped on its own (e.g. listener error) before cancellation.
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	<-serveErr // wait for Serve to return after Shutdown
	return nil
}

// browserURL turns a listener address into a loopback URL a browser can open.
// It rewrites a wildcard/unspecified host to localhost so "http://:7777" becomes
// "http://localhost:7777".
func browserURL(addr net.Addr) string {
	host := "localhost"
	port := ""
	if tcp, ok := addr.(*net.TCPAddr); ok {
		port = fmt.Sprintf("%d", tcp.Port)
		if tcp.IP != nil && !tcp.IP.IsUnspecified() {
			host = tcp.IP.String()
		}
	}
	if port == "" {
		return fmt.Sprintf("http://%s", host)
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

// OpenBrowser opens url in the platform's default browser. It returns an error
// if the launcher command cannot be started; it does not wait for the browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, bsd, etc.
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Start()
}
