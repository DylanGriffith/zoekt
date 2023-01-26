package main

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"reflect"
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

func TestIndexRepository(t *testing.T) {
	var cmdHistory [][]string

	executeCmd = func(ctx context.Context, name string, arg ...string) {
		currentCmd := append([]string{name}, arg...)
		cmdHistory = append(cmdHistory, currentCmd)
	}

	opts := Options{
		indexTimeout: CmdTimeout,
		repoDir:      "/repo_dir",
		indexDir:     "/index_dir",
	}

	req := indexRequest{
		CloneURL: "https://example.com/repository.git",
		RepoID:   100,
	}

	var w http.ResponseWriter
	indexRepository(opts, req, w)

	expectedHistory := make([][]string, 3)
	expectedHistory[0] = []string{"zoekt-git-clone", "-dest", "/repo_dir", "-name", "100", "-repoid", "100", "https://example.com/repository.git"}
	expectedHistory[1] = []string{"git", "-C", "/repo_dir/100.git", "fetch"}
	expectedHistory[2] = []string{"zoekt-git-index", "-index", "/index_dir", "/repo_dir/100.git"}

	if !reflect.DeepEqual(cmdHistory, expectedHistory) {
		t.Errorf("cmdHistory output is incorrect: %v, expected output: %v", cmdHistory, expectedHistory)
	}
}
