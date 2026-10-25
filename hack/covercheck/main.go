// Command covercheck enforces a minimum statement coverage on a Go cover
// profile, ignoring files that match -exclude.
//
//	go test -coverpkg=./internal/... -coverprofile=coverage.out ./...
//	go run ./hack/covercheck -profile coverage.out -min 80 -exclude '/internal/api/(gen|mock)/'
//
// With -coverpkg a block can appear once per test binary; a statement
// counts as covered if any binary covered it.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type block struct {
	stmts   int
	covered bool
}

func main() {
	profile := flag.String("profile", "coverage.out", "cover profile to read")
	minPct := flag.Float64("min", 80, "minimum total statement coverage, in percent")
	exclude := flag.String("exclude", "", "regexp of file paths to ignore")
	verbose := flag.Bool("v", false, "print coverage per package")
	flag.Parse()

	var re *regexp.Regexp
	if *exclude != "" {
		re = regexp.MustCompile(*exclude)
	}
	data, err := os.ReadFile(*profile)
	if err != nil {
		fail(err)
	}

	blocks, err := parse(bytes.NewReader(data), re)
	if err != nil {
		fail(err)
	}
	total, covered, perPkg := summarise(blocks)
	if total == 0 {
		fail(fmt.Errorf("%s: no statements after exclusions", *profile))
	}
	if *verbose {
		pkgs := make([]string, 0, len(perPkg))
		for p := range perPkg {
			pkgs = append(pkgs, p)
		}
		sort.Strings(pkgs)
		for _, p := range pkgs {
			c := perPkg[p]
			fmt.Printf("%6.1f%%  %s\n", pct(c[1], c[0]), p)
		}
	}
	got := pct(covered, total)
	fmt.Printf("total coverage: %.1f%% of %d statements (minimum %.1f%%)\n", got, total, *minPct)
	if got < *minPct {
		fmt.Fprintf(os.Stderr, "coverage %.1f%% is below the %.1f%% floor\n", got, *minPct)
		os.Exit(1)
	}
}

// parse reads a cover profile, merging duplicate blocks.
func parse(r io.Reader, exclude *regexp.Regexp) (map[string]*block, error) {
	blocks := make(map[string]*block)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		// file.go:start.col,end.col numStmts count
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed profile line %q", line)
		}
		file := fields[0][:strings.LastIndex(fields[0], ":")]
		if exclude != nil && exclude.MatchString(file) {
			continue
		}
		stmts, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("malformed profile line %q", line)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("malformed profile line %q", line)
		}
		b, ok := blocks[fields[0]]
		if !ok {
			b = &block{stmts: stmts}
			blocks[fields[0]] = b
		}
		b.covered = b.covered || count > 0
	}
	return blocks, sc.Err()
}

// summarise totals statements overall and per package directory; per
// package values are {total, covered}.
func summarise(blocks map[string]*block) (total, covered int, perPkg map[string][2]int) {
	perPkg = make(map[string][2]int)
	for key, b := range blocks {
		file := key[:strings.LastIndex(key, ":")]
		pkg := file[:strings.LastIndex(file, "/")]
		c := perPkg[pkg]
		c[0] += b.stmts
		total += b.stmts
		if b.covered {
			c[1] += b.stmts
			covered += b.stmts
		}
		perPkg[pkg] = c
	}
	return total, covered, perPkg
}

func pct(covered, total int) float64 {
	if total == 0 {
		return 100
	}
	return 100 * float64(covered) / float64(total)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "covercheck:", err)
	os.Exit(1)
}
