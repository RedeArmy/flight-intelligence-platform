// Command covmerge merges the blocks of a Go coverage profile that were reported more than once.
//
// `go test -coverpkg=./... ./...` runs one test binary per package and each one reports every package, so the same
// block appears many times in the combined profile, once per binary. Tools that read the profile (SonarCloud) may keep
// only one of those lines and under-report coverage. covmerge writes each block once, with the counts added up, so a
// block counts as covered when any test executed it.
//
// Usage: go run ./scripts/covmerge -in raw.out -out coverage.out
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	in := flag.String("in", "", "raw coverage profile written by go test")
	out := flag.String("out", "", "merged profile to write")
	flag.Parse()
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: covmerge -in raw.out -out coverage.out")
		os.Exit(2)
	}
	if err := mergeFiles(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "covmerge:", err)
		os.Exit(1)
	}
}

func mergeFiles(inPath, outPath string) error {
	src, err := os.Open(inPath) // #nosec G304 -- path given on the command line by the developer or CI
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(outPath) // #nosec G304 -- path given on the command line by the developer or CI
	if err != nil {
		return err
	}
	if err := merge(src, dst); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

// block is one coverage block: how many statements it has and how often it ran.
type block struct {
	stmts int
	count int64
}

// merge reads a coverage profile and writes it with every block once, counts summed, in a stable order.
func merge(r io.Reader, w io.Writer) error {
	mode, blocks, err := readProfile(r)
	if err != nil {
		return err
	}
	return writeProfile(w, mode, blocks)
}

func readProfile(r io.Reader) (string, map[string]block, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	mode := ""
	blocks := map[string]block{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if m, ok := strings.CutPrefix(line, "mode: "); ok {
			if mode != "" && mode != m {
				return "", nil, fmt.Errorf("mixed coverage modes %q and %q", mode, m)
			}
			mode = m
			continue
		}
		if err := addBlock(blocks, line); err != nil {
			return "", nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return "", nil, err
	}
	if mode == "" {
		return "", nil, errors.New("no 'mode:' line: not a coverage profile")
	}
	return mode, blocks, nil
}

// addBlock parses one block line and adds its count to the block already seen under the same key.
func addBlock(blocks map[string]block, line string) error {
	key, b, err := parseBlock(line)
	if err != nil {
		return err
	}
	cur, seen := blocks[key]
	if seen && cur.stmts != b.stmts {
		return fmt.Errorf("block %s has inconsistent statement counts (%d and %d): the profiles come from different code", key, cur.stmts, b.stmts)
	}
	blocks[key] = block{stmts: b.stmts, count: cur.count + b.count}
	return nil
}

func writeProfile(w io.Writer, mode string, blocks map[string]block) error {
	keys := make([]string, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "mode: %s\n", mode)
	for _, k := range keys {
		fmt.Fprintf(bw, "%s %d %d\n", k, blocks[k].stmts, blocks[k].count)
	}
	return bw.Flush()
}

// parseBlock splits "file:l.c,l.c stmts count" into its key (everything before the two numbers) and numbers.
func parseBlock(line string) (key string, b block, err error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", block{}, fmt.Errorf("malformed coverage line %q", line)
	}
	stmts, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", block{}, fmt.Errorf("malformed statement count in %q", line)
	}
	count, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return "", block{}, fmt.Errorf("malformed count in %q", line)
	}
	return fields[0], block{stmts: stmts, count: count}, nil
}
