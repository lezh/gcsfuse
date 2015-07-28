// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Write out a file of a certain size, close it, then measure the performance
// of doing the following:
//
// 1.  Open the file.
// 2.  Read it from start to end with a configurable buffer size.
//
package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"sort"
	"time"
)

var fDir = flag.String("dir", "", "Directory within which to write the file.")
var fDuration = flag.Duration("duration", 5*time.Second, "How long to run.")
var fFileSize = flag.Int64("file_size", 1<<20, "Size of file to use.")
var fReadSize = flag.Int64("read_size", 1<<14, "Size of each call to read(2).")

////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////

type DurationSlice []time.Duration

func (p DurationSlice) Len() int           { return len(p) }
func (p DurationSlice) Less(i, j int) bool { return p[i] < p[j] }
func (p DurationSlice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// REQUIRES: vals is sorted.
// REQUIRES: len(vals) > 0
// REQUIRES: 0 <= p <= 100
func percentile(
	vals DurationSlice,
	p int) (x time.Duration) {
	// We use the NIST method:
	//
	//     https://en.wikipedia.org/wiki/Percentile#NIST_method
	//
	// Begin by computing the rank.
	N := len(vals)
	rank := (float64(p) / 100) * float64(N+1)
	kFloat, d := math.Modf(rank)
	k := int(kFloat)

	// Handle each case.
	switch {
	case k == 0:
		x = vals[0]
		return

	case k >= N:
		x = vals[N-1]
		return

	case 0 < k && k < N:
		xFloat := float64(vals[k-1]) + d*float64(vals[k]-vals[k-1])
		x = time.Duration(xFloat)
		return

	default:
		panic("Invalid input")
	}
}

func formatBytes(v float64) string {
	switch {
	case v >= 1<<30:
		return fmt.Sprintf("%.2f GiB", v/(1<<30))

	case v >= 1<<20:
		return fmt.Sprintf("%.2f MiB", v/(1<<20))

	case v >= 1<<10:
		return fmt.Sprintf("%.2f KiB", v/(1<<10))

	default:
		return fmt.Sprintf("%.2f bytes", v)
	}
}

////////////////////////////////////////////////////////////////////////
// main logic
////////////////////////////////////////////////////////////////////////

func run() (err error) {
	if *fDir == "" {
		err = errors.New("You must set --dir.")
		return
	}

	// Create a temporary file.
	log.Printf("Creating a temporary file in %s.", *fDir)

	f, err := ioutil.TempFile(*fDir, "sequential_read")
	if err != nil {
		err = fmt.Errorf("TempFile: %v", err)
		return
	}

	path := f.Name()

	// Make sure we clean it up later.
	defer func() {
		log.Printf("Deleting %s.", path)
		os.Remove(path)
	}()

	// Fill it with random content.
	log.Printf("Writing %d random bytes.", *fFileSize)
	_, err = io.Copy(f, io.LimitReader(rand.Reader, *fFileSize))
	if err != nil {
		err = fmt.Errorf("Copying random bytes: %v", err)
		return
	}

	// Finish off the file.
	err = f.Close()
	if err != nil {
		err = fmt.Errorf("Closing file: %v", err)
		return
	}

	// Run several iterations.
	log.Printf("Measuring for %v...", *fDuration)

	var fullFileRead DurationSlice
	var singleReadCall DurationSlice
	buf := make([]byte, *fReadSize)

	overallStartTime := time.Now()
	for len(fullFileRead) == 0 || time.Since(overallStartTime) < *fDuration {
		// Open the file for reading.
		f, err = os.Open(path)
		if err != nil {
			err = fmt.Errorf("Opening file: %v", err)
			return
		}

		// Read the whole thing.
		fileStartTime := time.Now()
		for err == nil {
			readStartTime := time.Now()
			_, err = f.Read(buf)
			singleReadCall = append(singleReadCall, time.Since(readStartTime))
		}

		fullFileRead = append(fullFileRead, time.Since(fileStartTime))

		switch {
		case err == io.EOF:
			err = nil

		case err != nil:
			err = fmt.Errorf("Reading: %v", err)
			return
		}

		// Close the file.
		err = f.Close()
		if err != nil {
			err = fmt.Errorf("Closing file after reading: %v", err)
			return
		}
	}

	sort.Sort(fullFileRead)
	sort.Sort(singleReadCall)

	log.Printf(
		"Read the file %d times, using %d calls to read(2).",
		len(fullFileRead),
		len(singleReadCall))

	// Report.
	ptiles := []int{50, 90, 98}

	reportSlice := func(
		name string,
		bytesPerObservation int64,
		observations DurationSlice) {
		fmt.Printf("\n%s:\n", name)
		for _, ptile := range ptiles {
			d := percentile(observations, ptile)
			seconds := float64(d) / float64(time.Second)
			bandwidthBytesPerSec := float64(bytesPerObservation) / seconds

			fmt.Printf(
				"  %02dth ptile: %10v (%s/s)\n",
				ptile,
				d,
				formatBytes(bandwidthBytesPerSec))
		}
	}

	reportSlice("Full-file read times", *fFileSize, fullFileRead)
	reportSlice("read(2) latencies", *fReadSize, singleReadCall)

	fmt.Println()

	return
}

func main() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	flag.Parse()

	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
