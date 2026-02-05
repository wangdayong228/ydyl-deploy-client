package oscmdexec

import (
	"context"
	"os"
	"testing"
)

func TestOscmdexec(t *testing.T) {
	spec := Spec{
		Name: "node",
		Args: []string{"loop_print.js"},
		Dir:  ".",
		Env:  os.Environ(),
	}
	err := DefaultRunner(context.Background(), spec)
	if err != nil {
		t.Fatalf("DefaultRunner failed: %v", err)
	}
}
