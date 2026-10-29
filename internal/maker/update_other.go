//go:build !darwin

package maker

import (
	"context"
	"errors"
	"io/fs"
)

func InspectCard(ctx context.Context, id string) (CardInfo, error) {
	return CardInfo{}, errors.New("cards can only be read on macOS for now")
}

func UpdateCard(ctx context.Context, id string, all fs.FS, tools func() (string, error), report Progress) (CardInfo, error) {
	return CardInfo{}, errors.New("cards can only be updated on macOS for now")
}
