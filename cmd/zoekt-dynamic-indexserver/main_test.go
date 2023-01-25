package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

var (
	CmdTimeout = 100 * time.Millisecond
)

func captureOutput(f func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	f()
	log.SetOutput(os.Stderr)
	return buf.String()
}

func TestLoggedRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), CmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "-n", "1")

	stdout := captureOutput(func() {
		out, err := loggedRun(cmd)

		if len(err) != 0 {
			t.Errorf("err is not empty %v", err)
		}

		if string(out) != "1" {
			t.Errorf("command result is not equal to 1: %v", string(out))
		}
	})

	if !strings.Contains(stdout, "run [echo -n 1]") {
		t.Errorf("loggedRun output is incorrect: %v", stdout)
	}
}

func TestLoggedRunFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), CmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "foo")

	stdout := captureOutput(func() {
		loggedRun(cmd)
	})

	if !strings.Contains(stdout, "failed") {
		t.Errorf("loggedRun output is incorrect: %v", stdout)
	}
}
