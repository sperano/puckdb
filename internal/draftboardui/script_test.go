package draftboardui

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// clientTestScript is the browser client's Node test suite; it loads app.js
// into a fake DOM with deferred fetches (see web/harness_test.js).
const clientTestScript = "web/app_test.js"

// clientTestTimeout bounds the Node run, so a test stuck on a promise that is
// never resolved fails quickly instead of using up go test's whole budget.
const clientTestTimeout = time.Minute

// TestClientScript runs the browser client's tests with `node --test`, so
// `go test ./...` covers app.js. Without node the test is skipped locally but
// fails in CI (GitHub Actions sets CI), where the coverage must not vanish.
func TestClientScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("node is required in CI to run %s: %v", clientTestScript, err)
		}
		t.Skipf("node not installed; %s not run", clientTestScript)
	}
	ctx, cancel := context.WithTimeout(t.Context(), clientTestTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, node, "--test", clientTestScript).CombinedOutput()
	if err != nil {
		t.Fatalf("node --test %s: %v\n%s", clientTestScript, err, output)
	}
}
