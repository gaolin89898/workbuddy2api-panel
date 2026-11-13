//go:build !windows

package main

// attachParentConsole 非 Windows 平台没有控制台子系统差异，空实现。
func attachParentConsole() {}
