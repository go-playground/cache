package benchmarks

import (
	"os"
	"testing"

	"github.com/go-playground/cache/v2/internal/core"
)

// TestMain lets CACHE_TEST_ENGINE ("direct" or "concurrent") force v2's engine mode, to compare the modes at each
// core count; unset, v2 chooses automatically as it does in production.
func TestMain(m *testing.M) {
	switch os.Getenv("CACHE_TEST_ENGINE") {
	case "direct":
		core.SetModeForTesting(true)
	case "concurrent":
		core.SetModeForTesting(false)
	}
	os.Exit(m.Run())
}
