//go:build windows

package main

import "os"

func forwardedSignals() []os.Signal { return []os.Signal{os.Interrupt} }
