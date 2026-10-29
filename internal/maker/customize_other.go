//go:build !darwin

package maker

import (
	"context"
	"errors"
	"io/fs"
)

func Customize(ctx context.Context, image string, s Settings, payload fs.FS, report Progress) error {
	return errors.New("adapting the image is only supported on macOS for now")
}
