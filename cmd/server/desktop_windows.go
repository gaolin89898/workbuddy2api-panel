//go:build windows && desktop

package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

const desktopDefault = true

func prepareDesktop(configPath string, explicit bool) (string, func(), error) {
	var preferencesPath string
	if !explicit {
		executable, err := os.Executable()
		if err != nil {
			return "", nil, err
		}
		preferencesDir, err := os.UserConfigDir()
		if err != nil {
			return "", nil, err
		}
		preferencesPath = filepath.Join(preferencesDir, "WorkBuddyAccounts", "desktop-settings.json")
		configPath = desktopConfigPath(filepath.Dir(executable), preferencesPath)
	}
	path, err := filepath.Abs(configPath)
	if err != nil {
		return "", nil, err
	}
	if err = os.Chdir(filepath.Dir(path)); err != nil {
		return "", nil, err
	}
	if err = os.MkdirAll("data", 0700); err != nil {
		return "", nil, err
	}
	file, err := os.OpenFile(filepath.Join("data", "desktop.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return "", nil, err
	}
	log.SetOutput(file)
	log.Printf("desktop: 使用账号配置 %s", path)
	if preferencesPath != "" {
		if err := rememberDesktopConfig(preferencesPath, path); err != nil {
			log.Printf("desktop: 无法保存数据目录设置：%v", err)
		}
	}
	return path, func() { file.Close() }, nil
}

func reportDesktopError(err error) {
	log.Printf("desktop: %v", err)
	title, _ := windows.UTF16PtrFromString("账号控制台")
	message, _ := windows.UTF16PtrFromString(err.Error() + "\n\n详细日志：data/desktop.log")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
}

func runDesktop(configPath string, cfg *Config, gateway bool) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	version, err := webviewloader.GetInstalledVersion()
	if err != nil || version == "" {
		return fmt.Errorf("未检测到 Microsoft Edge WebView2 Runtime，请安装后重新打开桌面版。下载：https://developer.microsoft.com/microsoft-edge/webview2/")
	}
	origin, stop, err := startDesktopService(configPath, cfg, gateway)
	if err != nil {
		return err
	}
	defer stop()
	dataPath, err := filepath.Abs(stateSibling(cfg.StateFile, "webview2"))
	if err != nil {
		return err
	}
	w := webview.NewWithOptions(webview.WebViewOptions{
		DataPath: dataPath, AutoFocus: true,
		WindowOptions: webview.WindowOptions{Title: "账号控制台 · WorkBuddy / Trae / Qoder", Width: 1280, Height: 850, Center: true},
	})
	if w == nil {
		return fmt.Errorf("桌面窗口初始化失败，请检查 WebView2 Runtime")
	}
	defer w.Destroy()
	w.SetSize(760, 560, webview.HintMin)
	if err = w.Bind("desktopOpenExternal", func(raw string) error {
		if !validDesktopExternalURL(raw) {
			return fmt.Errorf("不支持的外部链接")
		}
		link, err := windows.UTF16PtrFromString(raw)
		if err != nil {
			return err
		}
		return windows.ShellExecute(0, nil, link, nil, nil, windows.SW_SHOWNORMAL)
	}); err != nil {
		return err
	}
	// The fragment is never sent to HTTP logs; the frontend removes it before mounting.
	w.Navigate(origin + "/panel/#desktop_key=" + url.QueryEscape(cfg.APIKey))
	log.Printf("desktop: 窗口已打开（%s），关闭窗口时退出桌面程序", origin)
	w.Run()
	log.Print("desktop: 窗口已关闭，正在结束桌面程序")
	return nil
}
