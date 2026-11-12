//go:build windows

package main

import "golang.org/x/sys/windows"

// hideConsoleWindow 隐藏当前控制台窗口，供 -silent 后台运行使用：
// 计划任务 / 脚本拉起时不再弹出黑色控制台框。进程本身继续留在
// 用户交互会话，保证账号切换等客户端 GUI 操作在当前桌面可见。
// 无控制台场景（Session 0、windowsgui 构建）下 GetConsoleWindow
// 返回 0，本函数是空操作。
func hideConsoleWindow() {
	// GetConsoleWindow 导出自 kernel32.dll；ShowWindow 导出自 user32.dll。
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	user32 := windows.NewLazySystemDLL("user32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	showWindow := user32.NewProc("ShowWindow")
	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd != 0 {
		const swHide = 0
		_, _, _ = showWindow.Call(hwnd, swHide)
	}
}
