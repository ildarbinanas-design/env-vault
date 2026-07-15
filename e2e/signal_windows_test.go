//go:build windows

package e2e_test

func testExecSignalForwarding(sc *scenario) {
	sc.t.Skip("expected platform skip: Windows runner contract supports console interrupts, not POSIX SIGTERM")
}
