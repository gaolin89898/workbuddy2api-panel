package doubao

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestBrowserLoginCapturesHttpOnlySession(t *testing.T) {
	if os.Getenv("DOUBAO_BROWSER_TEST") != "1" {
		t.Skip("set DOUBAO_BROWSER_TEST=1 to test the installed browser")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: "private-browser-session", Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "sessionid_ss", Value: "private-browser-session", Path: "/", HttpOnly: true})
		w.Write([]byte("<p>Signed in</p>"))
	}))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cookie, err := runBrowserLogin(ctx, BrowserPath(), fixture.URL, chromedp.Headless)
	if err != nil || !strings.Contains(cookie, "sessionid=private-browser-session") || !strings.Contains(cookie, "sessionid_ss=private-browser-session") {
		t.Fatalf("HttpOnly cookie capture failed: %v", err)
	}
}

func TestBrowserLoginCancellation(t *testing.T) {
	if os.Getenv("DOUBAO_BROWSER_TEST") != "1" {
		t.Skip("set DOUBAO_BROWSER_TEST=1 to test the installed browser")
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<p>Waiting</p>")) }))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	cookie, err := runBrowserLogin(ctx, BrowserPath(), fixture.URL, chromedp.Headless)
	if !errors.Is(err, context.DeadlineExceeded) || cookie != "" || time.Since(start) > 15*time.Second {
		t.Fatal("cancelled login left browser running or returned cookies")
	}
}
