package runner

// Windows LookPath uses PATHEXT rather than Unix execute permissions.
func pathHasNonExecutable(string) bool { return false }
