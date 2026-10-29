package maker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// command runs a tool and returns its output; on failure the error carries
// the command line and what it printed.
func command(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return out, nil
}
