//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestSecretSetStdinRefusesATerminal(t *testing.T) {
	storePath := setupTestBackend(t)
	controller, terminal := openPseudoTerminal(t)
	// Type a value and end of input (^D), so a regression that reads the
	// terminal stores the value and fails below instead of waiting forever.
	secretValue := testutil.EphemeralValue(t)
	if _, err := controller.Write([]byte(secretValue + "\n\x04")); err != nil {
		t.Fatalf("type into the pseudo-terminal: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "secret", "set", "nexus-token", "--stdin"}, terminal, &stdout, &stderr)
	if code != apperrors.ExitUsage {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var env output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != apperrors.CodeUsage || !strings.Contains(env.Error.Message, "stdin is a terminal") {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("the backend was written: %v", err)
	}
	testutil.AssertNotContains(t, "stdout", stdout.String(), secretValue)
	testutil.AssertNotContains(t, "stderr", stderr.String(), secretValue)
}

// openPseudoTerminal returns both sides of a new pseudo-terminal: the
// controller, which types into it, and the terminal a program reads.
func openPseudoTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	controller, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	fd := int(controller.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlock the pseudo-terminal: %v", err)
	}
	number, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("read the pseudo-terminal number: %v", err)
	}
	terminal, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open the pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { _ = terminal.Close() })
	return controller, terminal
}
