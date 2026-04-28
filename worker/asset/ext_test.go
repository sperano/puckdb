package asset

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		// Happy paths — lowercase extensions
		{name: "png lowercase", url: "https://example.com/foo.png", want: "png"},
		{name: "jpg lowercase", url: "https://example.com/foo.jpg", want: "jpg"},
		{name: "jpeg lowercase", url: "https://example.com/foo.jpeg", want: "jpeg"},
		{name: "gif lowercase", url: "https://example.com/foo.gif", want: "gif"},
		{name: "svg lowercase", url: "https://example.com/foo.svg", want: "svg"},
		{name: "webp lowercase", url: "https://example.com/foo.webp", want: "webp"},

		// Happy paths — case normalisation
		{name: "uppercase PNG", url: "https://example.com/FOO.PNG", want: "png"},
		{name: "mixed case extension", url: "https://example.com/image.Webp", want: "webp"},

		// Happy paths — URL decoration that should not affect extension
		{name: "with query string", url: "https://example.com/foo.png?v=12345", want: "png"},
		{name: "with fragment", url: "https://example.com/foo.png#x", want: "png"},
		{name: "multiple dots in filename", url: "https://example.com/foo.v2.png", want: "png"},

		// Happy paths — path depth
		{name: "deep path", url: "https://cdn.example.com/players/8478402/headshot.jpg", want: "jpg"},

		// Happy paths — percent-encoded characters in path
		{name: "percent-encoded spaces", url: "https://example.com/some%20file.png", want: "png"},

		// Boundary — trailing slash: path.Base strips it, so "foo.png/" → "foo.png" → "png".
		{name: "trailing slash after file", url: "https://example.com/foo.png/", want: "png"},

		// Error paths — no extension
		{name: "no extension", url: "https://example.com/path-no-extension", wantErr: true},

		// Error paths — unrecognised extensions
		{name: "aspx extension", url: "https://example.com/page.aspx", wantErr: true},
		{name: "php extension", url: "https://example.com/page.php", wantErr: true},
		{name: "html extension", url: "https://example.com/page.html", wantErr: true},
		{name: "txt extension", url: "https://example.com/readme.txt", wantErr: true},

		// Error paths — empty or host-only URL
		{name: "empty path root", url: "https://example.com/", wantErr: true},
		{name: "host only no path", url: "https://example.com", wantErr: true},

		// Error paths — malformed URL
		{name: "malformed URL", url: "http://[invalid", wantErr: true},

		// Error paths — numeric-only extension
		{name: "numeric extension", url: "https://example.com/file.123", wantErr: true},

		// Error paths — extension longer than 5 characters
		{name: "over-long extension", url: "https://example.com/file.toolongext", wantErr: true},

		// Boundary — mixed case with digits
		{name: "mixed case with digit", url: "https://example.com/image.JP2", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := extFromURL(tc.url)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
