package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Result struct {
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	StdoutMaxBytes  int
	StderrMaxBytes  int
	ExitCode        int
}

type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.max <= 0 {
		w.max = 100_000
	}
	if w.truncated {
		return len(p), nil
	}
	remain := w.max - w.buf.Len()
	if remain <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = w.buf.Write(p[:remain])
		w.truncated = true
		return len(p), nil
	}
	_, _ = w.buf.Write(p)
	return len(p), nil
}

func (w *limitedBuffer) String() string { return w.buf.String() }

func buildBwrapArgs(workDir string, argv []string) ([]string, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	workDir, _ = filepath.Abs(workDir)

	// Create a minimal filesystem view.
	args := []string{
		"--die-with-parent",
		"--unshare-net",
		"--unshare-pid",
		"--unshare-ipc",
		"--tmpfs", "/",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--dir", "/usr",
		"--ro-bind", "/usr", "/usr",
	}
	// Some distros still use /bin, /lib, /lib64. Bind if present.
	for _, p := range []string{"/bin", "/lib", "/lib64"} {
		args = append(args, "--dir", p)
		args = append(args, "--ro-bind-try", p, p)
	}
	// NOTE: we intentionally do NOT bind /etc to avoid leaking host config/secrets.

	args = append(args,
		"--bind", workDir, "/work",
		"--chdir", "/work",
		"--clearenv",
		"--setenv", "PATH", "/usr/bin:/bin",
		"--",
	)
	args = append(args, argv...)
	return args, nil
}

// RunNoNet runs a command in a bubblewrap sandbox with networking disabled.
//
// This is intentionally conservative and will evolve as we add per-user mounts
// (workspace/config/skills). For now it provides:
// - no network (bwrap --unshare-net)
// - writable working dir mounted at /work
// - /tmp as tmpfs
func RunNoNet(ctx context.Context, workDir string, argv []string) (Result, error) {
	if runtime.GOOS != "linux" {
		return Result{}, fmt.Errorf("sandbox only supported on linux")
	}
	args, err := buildBwrapArgs(workDir, argv)
	if err != nil {
		return Result{}, err
	}

	cmd := exec.CommandContext(ctx, "bwrap", args...)
	stdout := &limitedBuffer{max: 100_000}
	stderr := &limitedBuffer{max: 100_000}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	res := Result{
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
		StdoutMaxBytes:  stdout.max,
		StderrMaxBytes:  stderr.max,
		ExitCode:        0,
	}
	if err == nil {
		return res, nil
	}

	// Determine exit code. Non-zero exit is not treated as an execution error.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}
