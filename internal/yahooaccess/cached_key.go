package yahooaccess

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/sperano/puckdb/internal/resource"
)

// cachedGameKey returns the season's game key cached in the data path, the
// copy the importer reads before it calls Yahoo, with a note for the report.
// It returns 0 when no usable copy exists, and the note says why. A missing
// or malformed copy is a miss, as it is to the importer; any other read
// failure is returned as an error, since the importer would fail on it too.
func (c Checker) cachedGameKey(ctx context.Context, season int) (int, string, error) {
	if c.Storage == nil {
		return 0, "none: no data path configured", nil
	}
	gameKeyResource := resource.GameKey{Season: season}
	path := gameKeyResource.Path()
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	data, err := c.Storage.Read(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Sprintf("none: %s is not in the data path", path), nil
	}
	if err != nil {
		err = fmt.Errorf("read %s: %w", path, err)
		return 0, "unknown: " + err.Error(), err
	}
	key, err := gameKeyResource.ParseKey(data)
	if err != nil {
		return 0, fmt.Sprintf("none: %s: %v", path, err), nil
	}
	return key, fmt.Sprintf("%d, read from %s in the data path; the league calls use it", key, path), nil
}
