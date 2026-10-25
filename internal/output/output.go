// Package output prints command results as a table, TSV, JSON or YAML.
//
// JSON and YAML print the API resource shape and are semver-stable: fields
// may be added but never removed or retyped within a major version. Tables
// and TSV are for people and are not stable.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/itchyny/gojq"
	"sigs.k8s.io/yaml"

	"github.com/devlyft/cli/internal/clierr"
	"github.com/devlyft/cli/internal/iostreams"
)

// Format is an output format.
type Format string

// Output formats. FormatDefault is a table at a terminal and TSV otherwise.
const (
	FormatDefault Format = ""
	FormatTable   Format = "table"
	FormatTSV     Format = "tsv"
	FormatJSON    Format = "json"
	FormatYAML    Format = "yaml"
)

// Formats lists the values accepted by --format.
var Formats = []Format{FormatTable, FormatTSV, FormatJSON, FormatYAML}

// ParseFormat validates a --format value.
func ParseFormat(s string) (Format, error) {
	if s == "" {
		return FormatDefault, nil
	}
	for _, f := range Formats {
		if string(f) == s {
			return f, nil
		}
	}
	names := make([]string, len(Formats))
	for i, f := range Formats {
		names[i] = string(f)
	}
	return "", clierr.Usagef("invalid --format %q: must be one of %s", s, strings.Join(names, ", "))
}

// Options are the global output flags.
type Options struct {
	Format Format
	// JQ is a jq expression applied to the JSON output.
	JQ string
	// Quiet prints only resource IDs.
	Quiet bool
}

// Validate checks that the options can be combined.
func (o Options) Validate() error {
	if o.JQ != "" && o.Format != FormatDefault && o.Format != FormatJSON {
		return clierr.Usagef("--jq cannot be combined with --format %s", o.Format)
	}
	if o.Quiet && (o.JQ != "" || o.Format != FormatDefault) {
		return clierr.Usagef("--quiet cannot be combined with --format or --jq")
	}
	if o.JQ != "" {
		if _, err := gojq.Parse(o.JQ); err != nil {
			return clierr.Usagef("invalid --jq expression: %v", err)
		}
	}
	return nil
}

// IsJSON reports whether results, and errors, are printed as JSON.
func (o Options) IsJSON() bool { return o.Format == FormatJSON || o.JQ != "" }

// Table is the human-readable form of a result.
type Table struct {
	Headers []string
	Rows    [][]string
}

// Result is what a command prints.
type Result struct {
	// Data is the API resource or list of resources, printed by JSON, YAML
	// and --jq.
	Data any
	// Table is printed for the table and TSV formats. A nil Table falls
	// back to YAML, which suits single resources with nested fields.
	Table *Table
	// IDs are printed one per line by --quiet.
	IDs []string
}

// Printer writes results according to the options.
type Printer struct {
	ios  *iostreams.IOStreams
	opts Options
}

// NewPrinter returns a printer writing to ios.Out.
func NewPrinter(ios *iostreams.IOStreams, opts Options) *Printer {
	return &Printer{ios: ios, opts: opts}
}

// Options returns the printer's options.
func (p *Printer) Options() Options { return p.opts }

// Print writes r to stdout.
func (p *Printer) Print(r Result) error {
	w := p.ios.Out
	switch {
	case p.opts.Quiet:
		return writeLines(w, r.IDs)
	case p.opts.JQ != "":
		return writeJQ(w, p.opts.JQ, r.Data)
	}
	switch p.opts.Format {
	case FormatJSON:
		return writeJSON(w, r.Data)
	case FormatYAML:
		return writeYAML(w, r.Data)
	case FormatTSV:
		return p.writeTable(w, r, false)
	case FormatTable:
		return p.writeTable(w, r, true)
	case FormatDefault:
		return p.writeTable(w, r, p.ios.IsStdoutTTY())
	}
	return fmt.Errorf("unhandled format %q", p.opts.Format)
}

func (p *Printer) writeTable(w io.Writer, r Result, aligned bool) error {
	if r.Table == nil {
		return writeYAML(w, r.Data)
	}
	if !aligned {
		// TSV: no header, so `cut` and `while read` work directly.
		for _, row := range r.Table.Rows {
			if _, err := fmt.Fprintln(w, strings.Join(sanitizeRow(row), "\t")); err != nil {
				return err
			}
		}
		return nil
	}
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 3, ' ', 0)
	if _, err := fmt.Fprintln(tw, strings.ToUpper(strings.Join(r.Table.Headers, "\t"))); err != nil {
		return err
	}
	for _, row := range r.Table.Rows {
		if _, err := fmt.Fprintln(tw, strings.Join(sanitizeRow(row), "\t")); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	out := buf.String()
	if p.ios.ColorEnabled() {
		// Colour after alignment: escape codes would count as width.
		header, rest, _ := strings.Cut(out, "\n")
		out = "\x1b[1m" + strings.TrimRight(header, " ") + "\x1b[0m\n" + rest
	}
	_, err := io.WriteString(w, out)
	return err
}

// sanitizeRow keeps cell values from breaking the row structure.
func sanitizeRow(row []string) []string {
	out := make([]string, len(row))
	r := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")
	for i, c := range row {
		out[i] = r.Replace(c)
	}
	return out
}

func writeLines(w io.Writer, lines []string) error {
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func writeYAML(w io.Writer, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// writeJQ runs expr over v's JSON form. Strings print raw, like `jq -r`;
// other values print as compact JSON, one per line.
func writeJQ(w io.Writer, expr string, v any) error {
	q, err := gojq.Parse(expr)
	if err != nil {
		return clierr.Usagef("invalid --jq expression: %v", err)
	}
	input, err := normalize(v)
	if err != nil {
		return err
	}
	iter := q.Run(input)
	for {
		res, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, ok := res.(error); ok {
			if halt, ok := errors.AsType[*gojq.HaltError](err); ok && halt.Value() == nil {
				return nil
			}
			return clierr.Usagef("--jq: %v", err)
		}
		if s, ok := res.(string); ok {
			if _, err := fmt.Fprintln(w, s); err != nil {
				return err
			}
			continue
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(res); err != nil {
			return err
		}
		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
	}
}

// normalize converts v into the plain maps, slices and scalars gojq accepts,
// using its JSON encoding so field names match --format json.
func normalize(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
