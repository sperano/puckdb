// Package asset provides types and helpers for caching image assets from
// remote CDNs (NHL and Yahoo) onto the local JuiceFS-backed store.
package asset

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// validAssetExtensions is the strict allowlist of file extensions accepted
// for cached assets. No fallback extension is used; unknown extensions are
// hard errors so upstream drift is surfaced loudly.
var validAssetExtensions = map[string]struct{}{
	"png":  {},
	"jpg":  {},
	"jpeg": {},
	"gif":  {},
	"svg":  {},
	"webp": {},
}

// extFromURL extracts and validates the file extension from rawURL.
// It returns the lowercased extension (without leading dot) or an error
// if the URL cannot be parsed, has no path, has no extension, or the
// extension is not in validAssetExtensions.
func extFromURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}

	urlPath := parsed.Path
	if urlPath == "" || urlPath == "/" {
		return "", fmt.Errorf("URL has no file path: %q", rawURL)
	}

	segment := path.Base(urlPath)
	if segment == "." || segment == "/" {
		return "", fmt.Errorf("URL path has no file segment: %q", rawURL)
	}

	// path.Ext returns the extension including the leading dot, e.g. ".png".
	rawExt := path.Ext(segment)
	if rawExt == "" {
		return "", fmt.Errorf("URL path has no extension: %q", rawURL)
	}
	ext := strings.ToLower(strings.TrimPrefix(rawExt, "."))

	if _, ok := validAssetExtensions[ext]; !ok {
		return "", fmt.Errorf("unsupported asset extension %q in URL %q", ext, rawURL)
	}

	return ext, nil
}
