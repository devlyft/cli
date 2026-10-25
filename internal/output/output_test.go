package output

import (
	"testing"
)

func TestParseFormat(t *testing.T) {
	t.Parallel()

	for _, f := range append(Formats, FormatDefault) {
		if got, err := ParseFormat(string(f)); err != nil || got != f {
			t.Errorf("ParseFormat(%q) = %q, %v", f, got, err)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(xml) should fail")
	}
}

func TestOptionsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{"defaults", Options{}, false},
		{"jq implies json", Options{JQ: ".[]"}, false},
		{"jq with json", Options{Format: FormatJSON, JQ: ".[]"}, false},
		{"jq with yaml", Options{Format: FormatYAML, JQ: "."}, true},
		{"jq with table", Options{Format: FormatTable, JQ: "."}, true},
		{"invalid jq", Options{JQ: ".["}, true},
		{"quiet", Options{Quiet: true}, false},
		{"quiet with format", Options{Quiet: true, Format: FormatJSON}, true},
		{"quiet with jq", Options{Quiet: true, JQ: "."}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.opts.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
