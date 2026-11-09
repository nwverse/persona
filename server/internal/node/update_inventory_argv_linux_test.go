package node

import (
	"os"
	"slices"
	"testing"
)

func TestUpdateProcessKernelArgv(t *testing.T) {
	args, err := updateProcessArgv(os.Getpid())
	if err != nil || !slices.Equal(args, os.Args) {
		t.Fatalf("kernel arguments differ from the live process: %v", err)
	}
}
