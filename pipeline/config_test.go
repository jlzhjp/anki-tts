package pipeline

import (
	"strings"
	"testing"
)

func TestConfigValidationIsDeterministic(t *testing.T) {
	t.Parallel()
	config := Config{
		"z-last":  {Concurrency: -1},
		"a-first": {Concurrency: -1},
	}
	for range 20 {
		err := config.Validate()
		if err == nil || !strings.Contains(err.Error(), `stage "a-first"`) {
			t.Fatalf("error=%v", err)
		}
	}
}
