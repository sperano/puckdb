package draftboardui

import (
	"os"
	"os/exec"
	"testing"
)

// clientTestScript is the browser client's Node test suite; it loads app.js
// into a fake DOM with deferred fetches (see web/harness_test.js).
const clientTestScript = "web/app_test.js"

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
	output, err := exec.Command(node, "--test", clientTestScript).CombinedOutput()
	if err != nil {
		t.Fatalf("node --test %s: %v\n%s", clientTestScript, err, output)
	}
}
