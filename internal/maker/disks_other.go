//go:build !darwin && !windows

package maker

import (
	"context"
	"errors"
)

type Disk struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Protocol string `json:"protocol"`
}

var errMacOnly = errors.New("writing cards is only supported on macOS for now")

func ListDisks(ctx context.Context) ([]Disk, error)                          { return nil, errMacOnly }
func WriteCard(ctx context.Context, image, id string, report Progress) error { return errMacOnly }
