//go:build windows

package main

import (
	"log"
	"os"

	"golang.org/x/sys/windows"
)

// attachParentConsole windowsgui 构建的排障补救：从终端（Windows Terminal /
// conhost）手动启动时把日志接回父进程控制台，README 的终端启动流程不受影响。
// 双击、计划任务、登录 VBS、WMI 等拉起路径父进程没有控制台，AttachConsole
// 失败即静默返回——进程从不创建控制台窗口，无论默认终端是 conhost 还是
// Windows Terminal（后者已让 ShowWindow 式隐藏失效，见 silent_windows.go）。
func attachParentConsole() {
	const attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS = (DWORD)-1
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	attach := kernel32.NewProc("AttachConsole")
	if r, _, _ := attach.Call(attachParentProcess); r == 0 {
		return
	}
	// windowsgui 启动时标准句柄无效：重新打开 CONOUT$ 才能真正写控制台。
	out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	os.Stdout = out
	os.Stderr = out
	log.SetOutput(out)
}
