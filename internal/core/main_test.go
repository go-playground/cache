package core

import (
	"os"
	"testing"
)

// TestMain runs the package's tests against the engine mode selected by CACHE_TEST_ENGINE ("direct" or
// "concurrent"), or automatic selection when unset. CI runs every mode.
func TestMain(m *testing.M) {
	forceEngineFromEnv()
	os.Exit(m.Run())
}

func forceEngineFromEnv() {
	switch os.Getenv("CACHE_TEST_ENGINE") {
	case "direct":
		SetModeForTesting(true)
	case "concurrent":
		SetModeForTesting(false)
	}
}
