//go:build !darwin && !linux

package e2e_test

func testHiddenSecretInput(sc *scenario) {
	// The custom noncanonical editor applies only to macOS/Linux. The enclosing
	// lifecycle scenario still runs portable stdin size boundaries here.
}
