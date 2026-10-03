package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func wantErrorCode(sc *scenario, result commandResult, exit int, code string) {
	sc.t.Helper()
	wantExit(sc.t, result, exit)
	got := parseEnvelope(sc.t, result)
	if got.OK || got.Error == nil || got.Error.Code != code {
		sc.t.Fatalf("expected %s error, got %#v", code, got)
	}
}

func testEarlyErrorFormats(sc *scenario) {
	for _, args := range [][]string{
		{"--unknown", "--json"},
		{"secret", "--unknown", "--jsonl"},
		{"--unknown", "--json=false", "--jsonl=true"},
		{"unknown", "--json"},
		{"secret", "set", "--json"},
		{"secret", "delete", "--json"},
		{"profile", "add", "--json"},
		{"profile", "remove", "--json"},
		{"profile", "show", "--json"},
		{"export", "extra", "--json"},
		{"import", "--json"},
		{"--json", "--json=invalid", "version"},
	} {
		result := sc.run(args...)
		wantErrorCode(sc, result, 2, "USAGE")
		wantEmpty(sc.t, result.Stderr, "early JSON error stderr")
	}
	for _, args := range [][]string{
		{"unknown", "--", "--json"},
		{"--unknown", "--", "--json"},
		{"--unknown", "--config", "--json"},
		{"doctor", "--config", "--json", "extra"},
		{"--unknown", "--json=true", "--json=false"},
	} {
		result := sc.run(args...)
		wantExit(sc.t, result, 2)
		wantEmpty(sc.t, result.Stdout, "non-flag JSON argument stdout")
		wantContains(sc.t, result.Stderr, "code=USAGE", "non-flag JSON argument error")
	}
	wantErrorCode(sc, sc.run("--json", "exec", "--"), 2, "USAGE")
	wantErrorCode(sc, sc.run("--json", "exec", "first", "second", "--", sc.suite.helper), 2, "USAGE")
}

func testConfigFallbackIntegrity(sc *scenario) {
	cwd := filepath.Join(sc.root, "fallback")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		sc.t.Fatal(err)
	}
	options := runOptions{cwd: cwd}
	global := isolatedUserConfigPath(sc)
	wantExit(sc.t, sc.runWith(options, "--json", "profile", "create", "fallback", "--global"), 0)
	before := fileSHA256(sc.t, global)
	local := filepath.Join(cwd, ".env-vault.yaml")
	check := func() {
		for _, args := range [][]string{
			{"--json", "profile", "add", "fallback", "mapped:FALLBACK_TOKEN"},
			{"--json", "profile", "show", "fallback"},
			{"--json", "doctor"},
			{"--json", "exec", "fallback", "--", sc.suite.helper, "marker", "--path", filepath.Join(cwd, "child-started")},
		} {
			wantErrorCode(sc, sc.runWith(options, args...), 5, "CONFIG_INVALID")
			if fileSHA256(sc.t, global) != before {
				sc.t.Fatal("invalid local config changed the global config")
			}
		}
		if _, err := os.Stat(filepath.Join(cwd, "child-started")); !os.IsNotExist(err) {
			sc.t.Fatal("invalid local config launched a child")
		}
	}
	// A directory at the conventional local path must not select global.
	if err := os.Mkdir(local, 0o700); err != nil {
		sc.t.Fatal(err)
	}
	check()
	if err := os.Remove(local); err != nil {
		sc.t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, target := range []string{filepath.Join(cwd, "missing.yaml"), global} {
			if err := os.Symlink(target, local); err != nil {
				sc.t.Fatal(err)
			}
			check()
			if err := os.Remove(local); err != nil {
				sc.t.Fatal(err)
			}
		}
	}
	// Only confirmed absence restores the documented global fallback.
	wantExit(sc.t, sc.runWith(options, "--json", "profile", "add", "fallback", "mapped:FALLBACK_TOKEN"), 0)
	shown := sc.runWith(options, "--json", "profile", "show", "fallback")
	wantExit(sc.t, shown, 0)
	if envs := decodeProfileEnvs(sc.t, shown); len(envs) != 1 || envs[0] != "FALLBACK_TOKEN" {
		sc.t.Fatal("confirmed absence did not select global")
	}
}

func testConfigTrailingDocuments(sc *scenario) {
	path := filepath.Join(sc.root, "trailing-documents.yaml")
	for _, suffix := range []string{
		"---\nversion: 1\nprofiles:\n  retained: {}\n",
		"---\n",
		"---\nprofiles: [unterminated\n",
	} {
		original := []byte("version: 1\nprofiles:\n  dev: {}\n" + suffix)
		if err := os.WriteFile(path, original, 0o600); err != nil {
			sc.t.Fatal(err)
		}
		for _, args := range [][]string{
			{"profile", "create", "new"},
			{"profile", "add", "dev", "mapped:TOKEN"},
			{"profile", "show", "dev"},
			{"profile", "create", "new", "--dry-run"},
		} {
			full := append([]string{"--json", "--config", path}, args...)
			wantErrorCode(sc, sc.run(full...), 5, "CONFIG_INVALID")
			if after := sc.scanFile(path, "rejected multidocument config"); !bytes.Equal(original, after) {
				sc.t.Fatal("rejected trailing document was lost")
			}
		}
	}
}

func testMetadataConfigProtection(sc *scenario) {
	paths := []string{filepath.Join(sc.root, ".env-vault.yaml"), isolatedUserConfigPath(sc), filepath.Join(sc.root, "explicit-output-config.yaml")}
	for _, path := range paths {
		wantExit(sc.t, sc.run("--json", "--config", path, "profile", "create", "protected"), 0)
		before, lockBefore := fileSHA256(sc.t, path), fileSHA256(sc.t, path+".lock")
		for _, output := range []string{path, path + ".lock"} {
			for _, args := range [][]string{
				{"profile", "create", "new"},
				{"profile", "create", "new", "--dry-run"},
				{"profile", "show", "absent"},
				{"doctor", "extra"},
				{"--unknown"},
			} {
				full := append([]string{"--json", "--config", path, "--output", output}, args...)
				wantErrorCode(sc, sc.run(full...), 2, "USAGE")
				if fileSHA256(sc.t, path) != before || fileSHA256(sc.t, path+".lock") != lockBefore {
					sc.t.Fatal("metadata replaced config or lock")
				}
			}
		}
		if path != paths[2] {
			wantErrorCode(sc, sc.run("--json", "version", "--output", path), 2, "USAGE")
		}
	}
	// A metadata path whose parent is an ordinary file must fail safely.
	blocked := filepath.Join(sc.root, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("public fixture"), 0o600); err != nil {
		sc.t.Fatal(err)
	}
	result := sc.run("--json", "version", "--output", filepath.Join(blocked, "result.json"))
	got := parseEnvelope(sc.t, result)
	// Windows can report a missing path component where Unix reports ENOTDIR,
	// so either preflight or the atomic writer may detect the invalid parent.
	if got.OK || got.Error == nil || !(result.ExitCode == 2 && got.Error.Code == "USAGE" || result.ExitCode == 1 && got.Error.Code == "RUNTIME_ERROR") {
		sc.t.Fatal("invalid metadata parent was not rejected with a structured error")
	}
	if data := sc.scanFile(blocked, "invalid metadata parent"); string(data) != "public fixture" {
		sc.t.Fatal("invalid metadata output changed its parent file")
	}
	// Failure to resolve the user config directory cannot authorize output.
	wantErrorCode(sc, sc.runWith(runOptions{unset: []string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"}}, "--json", "version", "--output", filepath.Join(sc.root, "no-home.json")), 2, "USAGE")
}

func testTransferPathProtection(sc *scenario, container string) {
	before, storeBefore := fileSHA256(sc.t, container), fileSHA256(sc.t, sc.store)
	for _, args := range [][]string{
		{"export", "--out", container},
		{"export", "--out", container, "--force"},
		{"export", "--out", container, "--dry-run"},
		{"import", container},
		{"import", container, "--dry-run"},
		{"import", container, "--on-conflict", "invalid"},
		{"import", container, "extra"},
		{"import", container, "--unknown"},
	} {
		full := append([]string{"--json", "--output", container}, args...)
		// A random wrong passphrase makes the former import-auth-error
		// overwrite reproducible while preflight should now run first.
		wantErrorCode(sc, sc.runWith(runOptions{stdin: []byte(sc.newSentinel() + "\n")}, full...), 2, "USAGE")
		if fileSHA256(sc.t, container) != before || fileSHA256(sc.t, sc.store) != storeBefore {
			sc.t.Fatal("transfer metadata collision changed container or store")
		}
	}
	for _, name := range []string{"new-container.evb", "nested/new-container.evb"} {
		out := filepath.Join(sc.root, filepath.FromSlash(name))
		wantErrorCode(sc, sc.run("--json", "export", "--out", out, "--output", name), 2, "USAGE")
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			sc.t.Fatal("preflight created conflicting output")
		}
	}
	if runtime.GOOS != "windows" {
		alias := filepath.Join(sc.root, "container-alias.evb")
		if err := os.Link(container, alias); err != nil {
			sc.t.Fatal(err)
		}
		wantErrorCode(sc, sc.run("--json", "import", container, "--output", alias), 2, "USAGE")
		if fileSHA256(sc.t, alias) != before {
			sc.t.Fatal("hardlink alias changed")
		}
		deep := filepath.Join(sc.root, "real", "deep")
		if err := os.MkdirAll(deep, 0o700); err != nil {
			sc.t.Fatal(err)
		}
		link := filepath.Join(sc.root, "directory-alias")
		if err := os.Symlink(deep, link); err != nil {
			sc.t.Fatal(err)
		}
		// Keep symlink/.. intact; filepath.Join would erase the operation.
		wantErrorCode(sc, sc.run("--json", "export", "--out", "directory-alias/../planned.evb", "--output", "real/planned.evb", "--dry-run"), 2, "USAGE")
		broken := filepath.Join(sc.root, "dangling-output")
		if err := os.Symlink(filepath.Join(sc.root, "absent"), broken); err != nil {
			sc.t.Fatal(err)
		}
		wantErrorCode(sc, sc.run("--json", "version", "--output", broken), 2, "USAGE")
		wantErrorCode(sc, sc.run("--json", "export", "--out", broken), 2, "USAGE")
	}
	wantErrorCode(sc, sc.run("--json", "export", "--out", sc.root), 2, "USAGE")
}

func testExecPathPermissions(sc *scenario) {
	if runtime.GOOS == "windows" {
		return
	} // Windows executable discovery uses PATHEXT, not Unix mode bits.
	bin := filepath.Join(sc.root, "path-bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		sc.t.Fatal(err)
	}
	file := filepath.Join(bin, "blocked-command")
	if err := os.WriteFile(file, []byte("public nonexecutable fixture"), 0o600); err != nil {
		sc.t.Fatal(err)
	}
	options := runOptions{env: map[string]string{"PATH": bin}}
	for _, executable := range []string{"blocked-command", file} {
		wantErrorCode(sc, sc.runWith(options, "--json", "exec", "--", executable), 126, "COMMAND_NOT_EXECUTABLE")
	}
	// ErrDot still refuses an executable discovered through the current directory.
	dotExecutable := filepath.Join(sc.root, "dot-command")
	if err := os.WriteFile(dotExecutable, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		sc.t.Fatal(err)
	}
	wantErrorCode(sc, sc.runWith(runOptions{env: map[string]string{"PATH": "."}}, "--json", "exec", "--", "dot-command"), 127, "COMMAND_NOT_FOUND")
}

func testCorruptBackendErrors(sc *scenario) {
	if err := os.MkdirAll(sc.storeRoot, 0o700); err != nil {
		sc.t.Fatal(err)
	}
	if err := os.WriteFile(sc.store, []byte("invalid public test-backend framing"), 0o600); err != nil {
		sc.t.Fatal(err)
	}
	before := fileSHA256(sc.t, sc.store)
	for _, args := range [][]string{
		{"secret", "set", "unavailable", "--stdin"},
		{"secret", "check", "unavailable"},
		{"secret", "delete", "unavailable", "--confirm", "unavailable"},
		{"secret", "list"},
		{"export", "--out", filepath.Join(sc.root, "unavailable.evb")},
		{"profile", "add", "unused", "unavailable:TOKEN", "--check-secret"},
		{"exec", "--secret", "unavailable:TOKEN", "--", sc.suite.helper},
	} {
		full := append([]string{"--json"}, args...)
		wantErrorCode(sc, sc.runWith(runOptions{stdin: []byte(sc.sentinels[0] + "\n")}, full...), 4, "BACKEND_UNAVAILABLE")
		if fileSHA256(sc.t, sc.store) != before {
			sc.t.Fatal("backend error mutated the unreadable store")
		}
	}
	result := sc.run("--json", "doctor")
	wantExit(sc.t, result, 0)
	if len(parseEnvelope(sc.t, result).Warnings) == 0 {
		sc.t.Fatal("doctor omitted unreadable-store warning")
	}
	// Non-keychain fallback configuration stays within the isolated HOME.
	fallback := sc.runWith(runOptions{unset: []string{"APPDATA", "XDG_CONFIG_HOME"}}, "--json", "doctor")
	wantExit(sc.t, fallback, 0)
	if !strings.Contains(fallback.Stdout, "secret backend unavailable") {
		sc.t.Fatal("doctor lost backend warning while resolving fallback config")
	}
}

func testTransferInputValidation(sc *scenario, container, passphrase string) {
	storeBefore := fileSHA256(sc.t, sc.store)
	out := filepath.Join(sc.root, "invalid-passphrase.evb")
	generated := sc.newSentinel()
	short := generated[len(generated)-8:]
	// Track the shortened, runtime-generated value in every output/file scan.
	sc.sentinels = append(sc.sentinels, short)
	for _, input := range []string{"", short} {
		wantErrorCode(sc, sc.runWith(runOptions{stdin: []byte(input + "\n")}, "--json", "export", "--out", out), 2, "PASSPHRASE_INVALID")
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			sc.t.Fatal("invalid passphrase created a container")
		}
	}
	original := sc.scanFile(container, "input validation container")
	for _, change := range []struct {
		section, field string
		value          any
	}{
		{"", "schema", "unsupported.bundle"},
		{"", "version", 99},
		{"", "unknown_field", true},
		{"kdf", "algorithm", "unsupported"},
		{"cipher", "algorithm", "unsupported"},
		{"kdf", "key_length", 16},
		{"kdf", "salt", ""},
		{"cipher", "nonce", ""},
		{"", "payload", ""},
		{"kdf", "time", 0},
		{"kdf", "memory_kib", 0},
		{"kdf", "parallelism", 0},
	} {
		var document map[string]any
		if err := json.Unmarshal(original, &document); err != nil {
			sc.t.Fatal(err)
		}
		target := document
		if change.section != "" {
			target = document[change.section].(map[string]any)
		}
		target[change.field] = change.value
		encoded, err := json.Marshal(document)
		if err != nil {
			sc.t.Fatal(err)
		}
		path := filepath.Join(sc.root, "unsupported-header.evb")
		if err := os.WriteFile(path, encoded, 0o600); err != nil {
			sc.t.Fatal(err)
		}
		wantErrorCode(sc, sc.runWith(runOptions{stdin: []byte(passphrase + "\n")}, "--json", "import", path), 5, "BUNDLE_INVALID")
		if fileSHA256(sc.t, sc.store) != storeBefore {
			sc.t.Fatal("invalid container header changed the store")
		}
	}
	empty := filepath.Join(sc.root, "empty.evb")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		sc.t.Fatal(err)
	}
	wantErrorCode(sc, sc.runWith(runOptions{stdin: []byte(passphrase + "\n")}, "--json", "import", empty), 5, "BUNDLE_INVALID")
}
