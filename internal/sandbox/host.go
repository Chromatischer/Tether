package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// RunHost runs a command directly on the host, OUTSIDE the bubblewrap sandbox,
// inheriting the full host environment and filesystem (no namespace isolation,
// network available). This is intentionally unconfined and must only be reached
// through the admin.bash tool, which requires an explicit per-call user approval.
//
// Output is still capped via limitedBuffer so a runaway command cannot flood the
// model context. A non-zero exit status is returned as a Result, not an error.
func RunHost(ctx context.Context, workDir string, argv []string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("empty command")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	// Inherit the full host environment — this is the "all permissions" path.
	cmd.Env = os.Environ()

	stdout := &limitedBuffer{max: 100_000}
	stderr := &limitedBuffer{max: 100_000}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
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
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}
