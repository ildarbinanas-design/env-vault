package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/platform"
)

// Cobra parses flags before validating arguments. Preflight here also protects
// files when argument validation fails, before any command or error renderer
// can replace a config or container. Flag-parse failures never publish metadata:
// their incomplete paths cannot be checked safely.
func (a *App) protectCommandArguments(cmd *cobra.Command) {
	validate := cmd.Args
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if err := a.preflightMetadata(cmd, args); err != nil {
			return err
		}
		a.metadataReady = true
		a.metadataGuard = func() error { return a.preflightMetadata(cmd, args) }
		if validate != nil {
			if err := validate(cmd, args); err != nil {
				if _, ok := apperrors.From(err); ok {
					return err
				}
				return apperrors.Usage(commandID(cmd), err.Error(), "Run: "+cmd.CommandPath()+" --help")
			}
		}
		return nil
	}
	for _, child := range cmd.Commands() {
		a.protectCommandArguments(child)
	}
}

func (a *App) preflightMetadata(cmd *cobra.Command, args []string) error {
	if a.output.OutputPath == "" {
		return nil
	}
	paths := []string{config.LocalFile, a.configPath}
	global, err := platform.UserConfigPath()
	if err != nil {
		return apperrors.Usage(commandID(cmd), "Unable to check metadata output against the user config path", "Set the user config directory before using --output")
	}
	paths = append(paths, global)
	for _, path := range append([]string(nil), paths...) {
		if path != "" {
			paths = append(paths, path+".lock")
		}
	}
	switch cmd.Name() {
	case "export":
		out, _ := cmd.Flags().GetString("out")
		paths = append(paths, out)
	case "import":
		if len(args) > 0 {
			paths = append(paths, args[0])
		}
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		same, err := sameFilePath(a.output.OutputPath, path)
		if err != nil {
			return apperrors.Usage(commandID(cmd), "Unable to check metadata output path safely", "Use an accessible --output path separate from configs and containers")
		}
		if same {
			return apperrors.Usage(commandID(cmd), "Metadata output conflicts with a config or container path", "Choose a separate --output path")
		}
	}
	return nil
}

// Compare both existing file identities and canonical paths. Canonicalizing
// the nearest existing ancestor also catches directory symlink aliases when
// the file (or a parent directory) has not been created yet.
func sameFilePath(left, right string) (bool, error) {
	l, le := os.Stat(left)
	r, re := os.Stat(right)
	if le == nil && re == nil && os.SameFile(l, r) {
		return true, nil
	}
	lp, err := canonicalPath(left)
	if err != nil {
		return false, err
	}
	rp, err := canonicalPath(right)
	if err != nil {
		return false, err
	}
	// On platforms commonly using case-insensitive filesystems, reject case
	// aliases even before either output exists. A conservative rejection also
	// keeps these paths safe on a case-sensitive volume.
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.EqualFold(lp, rp), nil
	}
	return lp == rp, nil
}

func canonicalPath(path string) (string, error) {
	// Do not use Abs/Join/Clean before evaluating symlinks: link/../file
	// traverses the symlink before .. in the filesystem.
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		switch {
		case runtime.GOOS == "windows" && filepath.VolumeName(path) != "":
			// C:relative uses the current directory on that drive.
			volume := filepath.VolumeName(path)
			base, err := filepath.Abs(volume + ".")
			if err != nil {
				return "", err
			}
			path = base + string(filepath.Separator) + path[len(volume):]
		case runtime.GOOS == "windows" && len(path) > 0 && os.IsPathSeparator(path[0]):
			// \rooted uses the current drive, not the current directory.
			path = filepath.VolumeName(wd) + path
		default:
			path = wd + string(filepath.Separator) + path
		}
	}
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		// A dangling symlink is not a missing directory entry. Fail closed
		// instead of treating the symlink's spelling as a distinct target.
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", err
		}
		for len(path) > len(filepath.VolumeName(path))+1 && os.IsPathSeparator(path[len(path)-1]) {
			path = path[:len(path)-1]
		}
		parent, base := filepath.Split(path)
		if parent == path || base == "" {
			return "", err
		}
		tail = append(tail, base)
		path = parent
	}
}

// A flag parser stops at its first invalid flag. Recover only the presentation
// flags for that error, respecting known flag values and the -- boundary. Do
// not recover --output: an incomplete parse must never authorize a file write.
func (a *App) recoverErrorFormat(root *cobra.Command, args []string) {
	cmd, remaining, _ := root.Find(args)
	if cmd == nil {
		return
	}
	for i := 0; i < len(remaining); i++ {
		arg := remaining[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, value, hasValue := strings.Cut(arg[2:], "=")
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			continue
		}
		if !hasValue {
			if flag.NoOptDefVal == "" {
				i++
				continue
			}
			value = flag.NoOptDefVal
		}
		if name != "json" && name != "jsonl" {
			continue
		}
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			continue
		}
		if name == "json" {
			a.output.JSON = parsed
		} else {
			a.output.JSONL = parsed
		}
	}
}
