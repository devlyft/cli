// Command keyring-probe is a throwaway spike tool for devlyft/devlyft#261. It
// stores, reads back and deletes secrets of increasing size through
// zalando/go-keyring and reports the outcome and latency of each call, so we
// can see how the OS credential store behaves on headless Linux, in
// containers, under WSL, on macOS and on Windows.
//
// It is not part of the CLI and is never shipped.
package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	service = "devlyft-cli-keyring-probe"
	user    = "probe@example.com"
)

// base64url alphabet: JWTs and opaque tokens are ASCII from this set.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

var errTimeout = errors.New("timed out, call still blocked")

type result struct {
	Size   int    `json:"size"`
	Op     string `json:"op"`
	OK     bool   `json:"ok"`
	Millis int64  `json:"ms"`
	Err    string `json:"error,omitempty"`
}

type report struct {
	Env     map[string]string `json:"env"`
	Results []result          `json:"results"`
}

type outcome struct {
	value   string
	err     error
	elapsed time.Duration
}

func main() {
	timeout := flag.Duration("timeout", 5*time.Second, "per-call timeout (go-keyring calls cannot be cancelled)")
	sizes := flag.String("sizes", "512,1024,2048,2560,2561,3000,4096,8192", "comma-separated secret sizes in bytes")
	asJSON := flag.Bool("json", false, "print a JSON report instead of a table")
	flag.Parse()

	rep := report{Env: environment()}
	for _, n := range parseSizes(*sizes) {
		if !probe(&rep, n, *timeout) {
			break
		}
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	printTable(os.Stdout, &rep)
}

// probe runs set, get and delete for one secret size. It returns false when a
// call hung, because later calls would queue behind the blocked one.
func probe(rep *report, size int, timeout time.Duration) bool {
	secret := randomASCII(size)

	set := call(timeout, func() (string, error) { return "", keyring.Set(service, user, secret) })
	rep.Results = append(rep.Results, set.result(size, "set"))
	if set.err != nil {
		return !errors.Is(set.err, errTimeout)
	}

	get := call(timeout, func() (string, error) { return keyring.Get(service, user) })
	if get.err == nil && get.value != secret {
		get.err = fmt.Errorf("round trip mismatch: stored %d bytes, read %d", len(secret), len(get.value))
	}
	rep.Results = append(rep.Results, get.result(size, "get"))

	del := call(timeout, func() (string, error) { return "", keyring.Delete(service, user) })
	rep.Results = append(rep.Results, del.result(size, "delete"))
	return !errors.Is(get.err, errTimeout) && !errors.Is(del.err, errTimeout)
}

func (o outcome) result(size int, op string) result {
	r := result{Size: size, Op: op, OK: o.err == nil, Millis: o.elapsed.Milliseconds()}
	if o.err != nil {
		r.Err = fmt.Sprintf("%v (%T)", o.err, o.err)
	}
	return r
}

// call runs fn with a deadline. go-keyring has no context support, so on
// timeout the goroutine is abandoned.
func call(timeout time.Duration, fn func() (string, error)) outcome {
	done := make(chan outcome, 1)
	start := time.Now()
	go func() {
		v, err := fn()
		done <- outcome{value: v, err: err, elapsed: time.Since(start)}
	}()
	select {
	case o := <-done:
		return o
	case <-time.After(timeout):
		return outcome{err: errTimeout, elapsed: time.Since(start)}
	}
}

func parseSizes(s string) []int {
	var out []int
	for f := range strings.SplitSeq(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(f)); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func randomASCII(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func environment() map[string]string {
	env := map[string]string{
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
	}
	for _, k := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "CI", "GITHUB_ACTIONS", "WSL_DISTRO_NAME", "container"} {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		} else {
			env[k] = "<unset>"
		}
	}
	if b, err := os.ReadFile("/proc/version"); err == nil {
		env["wsl"] = strconv.FormatBool(strings.Contains(strings.ToLower(string(b)), "microsoft"))
	}
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			env["container_marker"] = f
		}
	}
	return env
}

func printTable(w io.Writer, rep *report) {
	for _, k := range slices.Sorted(maps.Keys(rep.Env)) {
		_, _ = fmt.Fprintf(w, "%s=%s\n", k, rep.Env[k])
	}
	_, _ = fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SIZE\tOP\tOK\tMS\tERROR")
	for _, r := range rep.Results {
		_, _ = fmt.Fprintf(tw, "%d\t%s\t%t\t%d\t%s\n", r.Size, r.Op, r.OK, r.Millis, r.Err)
	}
	_ = tw.Flush()
}
