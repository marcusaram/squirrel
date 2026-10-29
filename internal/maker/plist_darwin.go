package maker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// plist runs a macOS tool that prints a property list and decodes it.
func plist(ctx context.Context, v any, name string, args ...string) error {
	out, err := command(ctx, name, args...)
	if err != nil {
		return err
	}
	conv := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", "-")
	conv.Stdin = bytes.NewReader(out)
	js, err := conv.Output()
	if err != nil {
		return fmt.Errorf("plutil: %w", err)
	}
	return json.Unmarshal(js, v)
}
