// Copyright 2016 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This program manages a zoekt dynamic indexing deployment:
// * listens to indexing commands
// * reindexes specified repositories

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func loggedRun(cmd *exec.Cmd) (out, err []byte) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	log.Printf("run %v", cmd.Args)
	if err := cmd.Run(); err != nil {
		log.Printf("command %s failed: %v\nOUT: %s\nERR: %s",
			cmd.Args, err, outBuf.String(), errBuf.String())
	}

	return outBuf.Bytes(), errBuf.Bytes()
}

type Options struct {
	indexTimeout time.Duration
	dataDir      string
	indexDir     string
	repoDir      string
	listen       string
}

func (o *Options) createMissingDirectories() {
	for _, s := range []string{o.dataDir, o.indexDir, o.repoDir} {
		if _, err := os.Stat(s); err == nil {
			continue
		}

		if err := os.MkdirAll(s, 0o755); err != nil {
			log.Fatalf("MkdirAll %s: %v", s, err)
		}
	}
}

type indexRequest struct {
	CloneURL string // TODO: Decide if tokens can be in the URL or if we should pass separately
	RepoID   uint32
}

func startIndexingApi(opts Options) {
	http.HandleFunc("/index", serveIndex(opts))
	http.HandleFunc("/truncate", serveTruncate(opts))

	if err := http.ListenAndServe(opts.listen, nil); err != nil {
		log.Fatal(err)
	}
}

// This function is declared as var so that we can stub it in test
var executeCmd = func(ctx context.Context, name string, arg ...string) {
	cmd := exec.CommandContext(ctx, name, arg...)
	cmd.Stdin = &bytes.Buffer{}
	loggedRun(cmd)
}

func indexRepository(opts Options, req indexRequest, w http.ResponseWriter) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.indexTimeout)
	defer cancel()

	args := []string{}
	args = append(args, "-dest", opts.repoDir)
	args = append(args, "-name", strconv.FormatUint(uint64(req.RepoID), 10))
	args = append(args, "-repoid", strconv.FormatUint(uint64(req.RepoID), 10))
	args = append(args, req.CloneURL)
	executeCmd(ctx, "zoekt-git-clone", args...)

	gitRepoPath, err := filepath.Abs(filepath.Join(opts.repoDir, fmt.Sprintf("%d.git", req.RepoID)))
	if err != nil {
		log.Printf("error loading git repo path: %v", err)
		http.Error(w, "JSON parser error", http.StatusBadRequest)
		return
	}

	args = []string{
		"-C",
		gitRepoPath,
		"fetch",
	}
	executeCmd(ctx, "git", args...)

	args = []string{
		"-index", opts.indexDir,
		gitRepoPath,
	}
	executeCmd(ctx, "zoekt-git-index", args...)
}

func serveIndex(opts Options) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req indexRequest
		err := dec.Decode(&req)

		if err != nil {
			log.Printf("Error decoding index request: %v", err)
			http.Error(w, "JSON parser error", http.StatusBadRequest)
			return
		}

		indexRepository(opts, req, w)
	}
}

func serveTruncate(opts Options) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		err := emptyDirectory(opts.repoDir)

		if err != nil {
			log.Printf("Failed to empty repoDir repoDir: %v with error: %v", opts.repoDir, err)
			http.Error(w, "Failed to delete repoDir", http.StatusInternalServerError)
			return
		}

		err = emptyDirectory(opts.indexDir)

		if err != nil {
			log.Printf("Failed to empty repoDir indexDir: %v with error: %v", opts.repoDir, err)
			http.Error(w, "Failed to delete indexDir", http.StatusInternalServerError)
			return
		}
	}
}

func emptyDirectory(dir string) error {
	files, err := ioutil.ReadDir(dir)

	if err != nil {
		return err
	}

	for _, file := range files {
		filePath := filepath.Join(dir, file.Name())
		err := os.RemoveAll(filePath)
		if err != nil {
			return err
		}
	}

	return nil
}

func parseOptions() Options {
	dataDir := flag.String("data_dir", "", "directory holding all data.")
	indexDir := flag.String("index_dir", "", "directory holding index shards. Defaults to $data_dir/index/")
	timeout := flag.Duration("index_timeout", time.Hour, "kill index job after this much time")
	listen := flag.String("listen", ":6060", "listen on this address.")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("must set -data_dir")
	}

	if *indexDir == "" {
		*indexDir = filepath.Join(*dataDir, "index")
	}

	return Options{
		dataDir:      *dataDir,
		repoDir:      filepath.Join(*dataDir, "repos"),
		indexDir:     *indexDir,
		indexTimeout: *timeout,
		listen:       *listen,
	}
}

func main() {
	opts := parseOptions()

	// Automatically prepend our own path at the front, to minimize
	// required configuration.
	if l, err := os.Readlink("/proc/self/exe"); err == nil {
		os.Setenv("PATH", filepath.Dir(l)+":"+os.Getenv("PATH"))
	}

	opts.createMissingDirectories()

	startIndexingApi(opts)
}
