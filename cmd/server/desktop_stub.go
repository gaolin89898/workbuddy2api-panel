//go:build !windows || !desktop

package main

import "fmt"

const desktopDefault = false

func prepareDesktop(string, bool) (string, func(), error) {
	return "", nil, fmt.Errorf("请使用 Windows 桌面版 EXE")
}

func reportDesktopError(err error) { fmt.Println(err) }

func runDesktop(string, *Config, bool) error { return fmt.Errorf("desktop build required") }
