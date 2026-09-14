package apperrors

import (
	stderrors "errors"
	"testing"
)

func TestAppErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  *AppError
		want string
	}{
		{
			name: "nil receiver returns empty string",
			err:  nil,
			want: "",
		},
		{
			name: "formats code and message",
			err:  New("secret", CodeMissingSecret, "Missing secret: nexus-token", "Run: env-vault secret set nexus-token", ExitMissingSecret),
			want: "MISSING_SECRET: Missing secret: nexus-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppErrorUnwrap(t *testing.T) {
	t.Run("nil receiver returns nil", func(t *testing.T) {
		var err *AppError
		if got := err.Unwrap(); got != nil {
			t.Errorf("Unwrap() = %v, want nil", got)
		}
	})

	t.Run("returns wrapped cause", func(t *testing.T) {
		cause := stderrors.New("underlying failure")
		err := Wrap("profile", CodeConfigInvalid, "bad config", "fix it", ExitConfigInvalid, cause)
		if got := err.Unwrap(); got != cause {
			t.Errorf("Unwrap() = %v, want %v", got, cause)
		}
	})

	t.Run("nil cause returns nil", func(t *testing.T) {
		err := New("profile", CodeUsage, "bad usage", "fix it", ExitUsage)
		if got := err.Unwrap(); got != nil {
			t.Errorf("Unwrap() = %v, want nil", got)
		}
	})
}

func TestFrom(t *testing.T) {
	t.Run("matches an AppError", func(t *testing.T) {
		original := Usage("profile", "bad usage", "fix it")
		got, ok := From(original)
		if !ok {
			t.Fatal("From() ok = false, want true")
		}
		if got != original {
			t.Errorf("From() = %v, want %v", got, original)
		}
	})

	t.Run("matches an AppError wrapped by a plain error", func(t *testing.T) {
		original := Usage("profile", "bad usage", "fix it")
		wrapped := stderrors.Join(stderrors.New("context"), original)
		got, ok := From(wrapped)
		if !ok {
			t.Fatal("From() ok = false, want true")
		}
		if got != original {
			t.Errorf("From() = %v, want %v", got, original)
		}
	})

	t.Run("plain error does not match", func(t *testing.T) {
		_, ok := From(stderrors.New("plain error"))
		if ok {
			t.Error("From() ok = true, want false")
		}
	})
}

func TestExitStatus(t *testing.T) {
	t.Run("Error formats exit code", func(t *testing.T) {
		status := NewExitStatus(127)
		if got, want := status.Error(), "exit status 127"; got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("ExitStatusFrom matches an ExitStatus", func(t *testing.T) {
		original := NewExitStatus(126)
		got, ok := ExitStatusFrom(original)
		if !ok {
			t.Fatal("ExitStatusFrom() ok = false, want true")
		}
		if got != original {
			t.Errorf("ExitStatusFrom() = %v, want %v", got, original)
		}
	})

	t.Run("ExitStatusFrom rejects a plain error", func(t *testing.T) {
		_, ok := ExitStatusFrom(stderrors.New("plain error"))
		if ok {
			t.Error("ExitStatusFrom() ok = true, want false")
		}
	})
}
