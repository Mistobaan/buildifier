/*
Copyright 2016 Google Inc. All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

// Buildifier, a tool to parse and format BUILD files.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	build "github.com/bazelbuild/buildifier/core"
	"github.com/bazelbuild/buildifier/differ"
)

var (
	// Undocumented; for debugging.
	showlog = flag.Bool("showlog", false, "show log in check mode")

	vflag = flag.Bool("v", false, "print verbose information on standard error")
	dflag = flag.Bool("d", false, "alias for -mode=diff")
	mode  = flag.String("mode", "", "formatting mode: check, diff, or fix (default fix)")
	path  = flag.String("path", "", "assume BUILD file has this path relative to the workspace directory")

	// Debug flags passed through to rewrite.go
	allowSort = stringList("allowsort", "additional sort contexts to treat as safe")
	disable   = stringList("buildifier_disable", "list of buildifier rewrites to disable")
)

func stringList(name, help string) func() []string {
	f := flag.String(name, "", help)
	return func() []string {
		return strings.Split(*f, ",")
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: buildifier [-d] [-v] [-mode=mode] [-path=path] [files...]

Buildifier applies a standard formatting to the named BUILD files.
The mode flag selects the processing: check, diff, or fix.
In check mode, buildifier prints a list of files that need reformatting.
In diff mode, buildifier shows the diffs that it would make.
In fix mode, buildifier updates the files that need reformatting and,
if the -v flag is given, prints their names to standard error.
The default mode is fix. -d is an alias for -mode=diff.

If no files are listed, buildifier reads a BUILD file from standard input. In
fix mode, it writes the reformatted BUILD file to standard output, even if no
changes are necessary.

Buildifier's reformatting depends in part on the path to the file relative
to the workspace directory. Normally buildifier deduces that path from the
file names given, but the path can be given explicitly with the -path
argument. This is especially useful when reformatting standard input,
or in scripts that reformat a temporary copy of a file.
`)
	os.Exit(2)
}

func main() {
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()

	// Pass down debug flags into build package
	build.DisableRewrites = disable()
	build.AllowSort = allowSort()

	if *dflag {
		if *mode != "" {
			fmt.Fprintf(os.Stderr, "buildifier: cannot specify both -d and -mode flags\n")
			os.Exit(2)
		}
		*mode = "diff"
	}

	// Check mode.
	switch *mode {
	default:
		fmt.Fprintf(os.Stderr, "buildifier: unrecognized mode %s; valid modes are check, diff, fix\n", *mode)
		os.Exit(2)

	case "":
		*mode = "fix"

	case "check", "diff", "fix":
		// ok
	}

	// If the path flag is set, must only be formatting a single file.
	// It doesn't make sense for multiple files to have the same path.
	if *path != "" && len(args) > 1 {
		fmt.Fprintf(os.Stderr, "buildifier: can only format one file when using -path flag\n")
		os.Exit(2)
	}

	diff = differ.Find()

	// TODO(bazel-team): Handle "-" as stdin/stdout mode too.

	if len(args) == 0 {
		// Read from stdin, write to stdout.
		if *mode == "fix" {
			*mode = "pipe"
		}
		processFiles([]string{"stdin"})
	} else {
		processFiles(args)
	}

	diff.Run()

	for _, file := range toRemove {
		os.Remove(file)
	}

	os.Exit(exitCode)
}

func processFiles(files []string) {
	// Start nworker workers reading stripes of the input
	// argument list and sending the resulting data on
	// separate channels. file[k] is read by worker k%nworker
	// and delivered on ch[k%nworker].
	type result struct {
		file string
		err  error
	}

	if len(files) == 0 {
		// nothing to process
		return
	}

	var wg sync.WaitGroup

	in := make(chan string)
	ch := make(chan result, len(files))

	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func() {
			for filename := range in {
				err := processFile(filename)
				if err != nil {
					ch <- result{filename, err}
				}
			}
			wg.Done()
		}()
	}
	for _, fname := range files {
		in <- fname
	}
	close(in)
	wg.Wait()
	close(ch)

	for res := range ch {
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "buildifier: %v\n", res.err)
			continue
		}
	}
}

// exitCode is the code to use when exiting the program.
// The codes used by buildifier are:
//
// 0: success, everything went well
// 1: syntax errors in input
// 2: usage errors: invoked incorrectly
// 3: unexpected runtime errors: file I/O problems or internal bugs
var exitCode = 0

// toRemove is a list of files to remove before exiting.
var toRemove []string

// diff is the differ to use when *mode == "diff".
var diff *differ.Differ

// processFile processes a single file containing data.
// It has been read from filename and should be written back if fixing.
func processFile(filename string) error {
	var (
		data []byte
		err  error
	)

	if filename == "stdin" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(filename)
	}
	if err != nil {
		return err
	}

	f, err := build.Parse(filename, data)
	if err != nil {

		return nil
	}

	if *path != "" {
		f.Path = *path
	}
	beforeRewrite := build.Format(f)
	var info build.RewriteInfo
	build.Rewrite(f, &info)
	ndata := build.Format(f)

	switch *mode {
	case "check":
		// check mode: print names of files that need formatting.
		if !bytes.Equal(data, ndata) {
			// Print:
			//	name # list of what changed
			reformat := ""
			if !bytes.Equal(data, beforeRewrite) {
				reformat = " reformat"
			}
			var log string

			if len(info.Log) > 0 && *showlog {
				sort.Strings(info.Log)
				var uniq []string
				var last string
				for _, s := range info.Log {
					if s != last {
						last = s
						uniq = append(uniq, s)
					}
				}
				log = " " + strings.Join(uniq, " ")
			}
			fmt.Printf("%s #%s %s%s\n", filename, reformat, &info, log)
		}
		return nil

	case "diff":
		// diff mode: run diff on old and new.
		if bytes.Equal(data, ndata) {
			return nil
		}
		outfile, err := writeTemp(ndata)
		if err != nil {
			return err
		}
		infile := filename
		if filename == "" {
			// data was read from standard filename.
			// Write it to a temporary file so diff can read it.
			infile, err = writeTemp(data)
			if err != nil {
				return err
			}
		}
		diff.Show(infile, outfile)

	case "pipe":
		// pipe mode - reading from stdin, writing to stdout.
		// ("pipe" is not from the command line; it is set above in main.)
		os.Stdout.Write(ndata)
		return nil

	case "fix":
		// fix mode: update files in place as needed.
		if bytes.Equal(data, ndata) {
			return nil
		}

		err := ioutil.WriteFile(filename, ndata, 0666)
		if err != nil {
			return err
		}

		if *vflag {
			fmt.Fprintf(os.Stderr, "fixed %s\n", filename)
		}
	default:
		panic("mode not supported: " + *mode)
	}
	return nil
}

// writeTemp writes data to a temporary file and returns the name of the file.
func writeTemp(data []byte) (file string, err error) {
	f, err := ioutil.TempFile("", "buildifier-tmp-")
	if err != nil {
		return "", fmt.Errorf("creating temporary file: %v", err)
	}
	name := f.Name()
	toRemove = append(toRemove, name)
	defer f.Close()
	_, err = f.Write(data)
	if err != nil {
		return "", fmt.Errorf("writing temporary file: %v", err)
	}
	return name, nil
}
