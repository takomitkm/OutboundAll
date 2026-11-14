package controller

import (
	"os"
	"os/exec"
)

func execCommand(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

var _ = os.FileMode(0)
