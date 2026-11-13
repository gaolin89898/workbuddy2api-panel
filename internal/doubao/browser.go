package doubao

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/linguo2625469/workbuddy2api-panel/internal/traelogin"
)

var ErrBrowserLaunch = errors.New("无法打开豆包登录窗口，请检查浏览器和网络后重试")
var ErrBrowserClosed = errors.New("豆包登录窗口已关闭，请重新开始登录")

// Prefer Chrome: some Edge installations disable the debugging protocol used
// to read the completed login session. Fall back to the shared browser search.
func BrowserPath() string {
	var candidates []string
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	} else if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	}
	if runtime.GOOS != "linux" || os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "" {
		for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return traelogin.BrowserPath()
}

// RunBrowserLogin uses a temporary browser profile and only reads cookies scoped
// to the official Doubao origin. HttpOnly session cookies never enter the UI.
func RunBrowserLogin(ctx context.Context, executable string) (string, error) {
	return runBrowserLogin(ctx, executable, BaseURL+"/chat/")
}

func runBrowserLogin(ctx context.Context, executable, target string, extra ...chromedp.ExecAllocatorOption) (string, error) {
	opts := []chromedp.ExecAllocatorOption{chromedp.ExecPath(executable), chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("remote-debugging-address", "127.0.0.1"), chromedp.Flag("remote-debugging-port", "0"), chromedp.Flag("window-size", "1050,800"), chromedp.Flag("no-sandbox", false)}
	opts = append(opts, extra...)
	allocator, stopAllocator := chromedp.NewExecAllocator(ctx, opts...)
	defer stopAllocator()
	browser, stopBrowser := chromedp.NewContext(allocator, chromedp.WithErrorf(func(string, ...any) {}))
	defer stopBrowser()
	if err := chromedp.Run(browser, chromedp.Navigate(target)); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrBrowserLaunch
	}
	u, _ := url.Parse(target)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-browser.Done():
			return "", ErrBrowserClosed
		case <-ticker.C:
			var cookies []*network.Cookie
			err := chromedp.Run(browser, chromedp.ActionFunc(func(ctx context.Context) error {
				var err error
				cookies, err = network.GetCookies().WithUrls([]string{u.Scheme + "://" + u.Host + "/chat/"}).Do(ctx)
				return err
			}))
			if err != nil {
				continue
			}
			parts := []string{}
			for _, cookie := range cookies {
				if cookie.Value != "" {
					parts = append(parts, cookie.Name+"="+cookie.Value)
				}
			}
			if cookie, err := NormalizeCookie(strings.Join(parts, "; ")); err == nil {
				return cookie, nil
			}
		}
	}
}
