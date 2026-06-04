package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestServe_ServesHTMLAndShutsDown verifies AC-7's core: the server serves the
// given HTML, opens the browser, and returns nil after the context is cancelled
// (the Ctrl+C path), without leaking the listener.
func TestServe_ServesHTMLAndShutsDown(t *testing.T) {
	html := []byte("<!DOCTYPE html><html><body>hello llmlore</body></html>")

	var (
		mu         sync.Mutex
		openedURL  string
		openCalled bool
	)
	opener := func(url string) error {
		mu.Lock()
		defer mu.Unlock()
		openedURL = url
		openCalled = true
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// Port 0 lets the OS pick a free port; the browser opener captures the URL.
		done <- Serve(ctx, "127.0.0.1:0", html, opener, nil)
	}()

	// Wait for the browser opener to fire, which happens only after the listener
	// is up. This avoids racing the goroutine.
	url := waitForURL(t, &mu, &openCalled, &openedURL)

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(body) != string(html) {
		t.Errorf("body = %q, want %q", body, html)
	}
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}

	// Cancelling the context must trigger a clean shutdown returning nil.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return within 5s of context cancel")
	}

	// The listener must be released: a fresh GET should now fail to connect.
	if _, err := http.Get(url); err == nil {
		t.Errorf("server still reachable after shutdown")
	}
}

// TestServe_BrowserFailureNonFatal ensures a browser that cannot open does not
// fail the server: it still serves and shuts down cleanly.
func TestServe_BrowserFailureNonFatal(t *testing.T) {
	html := []byte("ok")
	var (
		mu        sync.Mutex
		called    bool
		capturedU string
	)
	opener := func(url string) error {
		mu.Lock()
		defer mu.Unlock()
		called = true
		capturedU = url
		return errNoBrowser
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, "127.0.0.1:0", html, opener, nil) }()

	url := waitForURL(t, &mu, &called, &capturedU)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v despite browser failure, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestServe_ListenError(t *testing.T) {
	// Occupy a port, then ask Serve to bind the same address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pre-listen: %v", err)
	}
	defer ln.Close()

	err = Serve(context.Background(), ln.Addr().String(), []byte("x"), func(string) error { return nil }, nil)
	if err == nil {
		t.Errorf("Serve on an occupied address should return an error")
	}
}

func TestBrowserURL(t *testing.T) {
	cases := []struct {
		addr net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.IPv4zero, Port: 7777}, "http://localhost:7777"},
		{&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}, "http://127.0.0.1:8080"},
	}
	for _, c := range cases {
		if got := browserURL(c.addr); got != c.want {
			t.Errorf("browserURL(%v) = %q, want %q", c.addr, got, c.want)
		}
	}
}

var errNoBrowser = &browserErr{}

type browserErr struct{}

func (*browserErr) Error() string { return "no browser" }

// waitForURL polls until the browser opener has captured a URL, then returns it.
func waitForURL(t *testing.T, mu *sync.Mutex, called *bool, url *string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if *called {
			u := *url
			mu.Unlock()
			return u
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("browser opener was never called (server may not have started)")
	return ""
}
