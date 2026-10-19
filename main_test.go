package cache_test

import (
	"os"
	"testing"

	"github.com/go-playground/cache/v2/internal/core"
)

// TestMain runs the package's tests against the engine mode selected by CACHE_TEST_ENGINE ("direct" or
// "concurrent"), or automatic selection when unset. CI runs every mode.
func TestMain(m *testing.M) {
	switch os.Getenv("CACHE_TEST_ENGINE") {
	case "direct":
		core.SetModeForTesting(true)
	case "concurrent":
		core.SetModeForTesting(false)
	}
	os.Exit(m.Run())
}
