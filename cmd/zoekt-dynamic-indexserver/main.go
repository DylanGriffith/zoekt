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

// This program manages a zoekt indexing deployment:
// * recycling logs
// * periodically fetching new data.
// * periodically reindexing all git repos.

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

const day = time.Hour * 24

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
}

func (o *Options) defineFlags() {
	flag.DurationVar(&o.indexTimeout, "index_timeout", time.Hour, "kill index job after this much time")
}

// fetchGitRepo runs git-fetch, and returns true if there was an
// update.
func fetchGitRepo(dir string) bool {
	cmd := exec.Command("git", "--git-dir", dir, "fetch", "origin")
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	// Prevent prompting
	cmd.Stdin = &bytes.Buffer{}
	cmd.Stderr = errBuf
	cmd.Stdout = outBuf
	if err := cmd.Run(); err != nil {
		log.Printf("command %s failed: %v\nOUT: %s\nERR: %s",
			cmd.Args, err, outBuf.String(), errBuf.String())
	} else {
		return len(outBuf.Bytes()) != 0
	}
	return false
}

type indexRequest struct {
	CloneURL string // TODO: Decide if tokens can be in the URL or if we should pass separately
	RepoID   uint32
}

func startIndexingApi(repoDir string, indexDir string, listen string, indexTimeout time.Duration) {
	http.HandleFunc("/index", func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var req indexRequest
		err := dec.Decode(&req)

		if err != nil {
			log.Printf("Error decoding index request: %v", err)
			http.Error(w, "JSON parser error", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), indexTimeout)

		defer cancel()

		args := []string{}
		args = append(args, "-dest", repoDir)
		args = append(args, "-name", strconv.FormatUint(uint64(req.RepoID), 10))
		args = append(args, "-repoid", strconv.FormatUint(uint64(req.RepoID), 10))
		args = append(args, req.CloneURL)
		cmd := exec.CommandContext(ctx, "zoekt-git-clone", args...)
		cmd.Stdin = &bytes.Buffer{}
		loggedRun(cmd)

		args = []string{}

		gitRepoPath, err := filepath.Abs(filepath.Join(repoDir, fmt.Sprintf("%d.git", req.RepoID)))
		if err != nil {
			log.Printf("error loading git repo path: %v", err)
			http.Error(w, "JSON parser error", http.StatusBadRequest)
			return
		}
		args = append(args, "-C", gitRepoPath, "fetch")
		cmd = exec.CommandContext(ctx, "git", args...)
		cmd.Stdin = &bytes.Buffer{}
		loggedRun(cmd)

		args = []string{}

		args = append(args, gitRepoPath)
		cmd = exec.CommandContext(ctx, "zoekt-git-index", args...)
		cmd.Dir = indexDir
		cmd.Stdin = &bytes.Buffer{}
		loggedRun(cmd)
	})

	http.HandleFunc("/truncate", func(w http.ResponseWriter, r *http.Request) {
		err := emptyDirectory(repoDir)

		if err != nil {
			log.Printf("Failed to empty repoDir repoDir: %v with error: %v", repoDir, err)
			http.Error(w, "Failed to delete repoDir", http.StatusInternalServerError)
			return
		}

		err = emptyDirectory(indexDir)

		if err != nil {
			log.Printf("Failed to empty repoDir indexDir: %v with error: %v", repoDir, err)
			http.Error(w, "Failed to delete indexDir", http.StatusInternalServerError)
			return
		}
	})

	if err := http.ListenAndServe(listen, nil); err != nil {
		log.Fatal(err)
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

func main() {
	var opts Options
	opts.defineFlags()
	dataDir := flag.String("data_dir",
		filepath.Join(os.Getenv("HOME"), "zoekt-serving"), "directory holding all data.")
	indexDir := flag.String("index_dir", "", "directory holding index shards. Defaults to $data_dir/index/")
	listen := flag.String("listen", ":6060", "listen on this address.")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("must set --data_dir")
	}

	// Automatically prepend our own path at the front, to minimize
	// required configuration.
	if l, err := os.Readlink("/proc/self/exe"); err == nil {
		os.Setenv("PATH", filepath.Dir(l)+":"+os.Getenv("PATH"))
	}

	logDir := filepath.Join(*dataDir, "logs")
	if *indexDir == "" {
		*indexDir = filepath.Join(*dataDir, "index")
	}
	repoDir := filepath.Join(*dataDir, "repos")
	for _, s := range []string{logDir, *indexDir, repoDir} {
		if _, err := os.Stat(s); err == nil {
			continue
		}

		if err := os.MkdirAll(s, 0o755); err != nil {
			log.Fatalf("MkdirAll %s: %v", s, err)
		}
	}

	startIndexingApi(repoDir, *indexDir, *listen, opts.indexTimeout)
}
