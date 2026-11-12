//go:build !windows

package main

// hideConsoleWindow 非 Windows 平台没有控制台窗口概念，空实现。
func hideConsoleWindow() {}
