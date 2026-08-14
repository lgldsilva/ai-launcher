package shlex

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr string
	}{
		{name: "empty", input: "   \t", want: nil},
		{name: "bare words", input: "--verbose --model sonnet", want: []string{"--verbose", "--model", "sonnet"}},
		{name: "quoted spaces", input: `--model "sonnet 4" 'hello world'`, want: []string{"--model", "sonnet 4", "hello world"}},
		{name: "double quote escape", input: `--message "say \"hi\""`, want: []string{"--message", `say "hi"`}},
		{name: "single quote backslash literal", input: `'C:\\work'`, want: []string{`C:\\work`}},
		{name: "empty quoted value", input: `--value ""`, want: []string{"--value", ""}},
		{name: "trailing escape", input: `--value\`, wantErr: "trailing escape"},
		{name: "unterminated quote", input: `--value "text`, wantErr: "unterminated quote"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Split(tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Split(%q) error = %v; want %q", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Split(%q) error = %v", tt.input, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Split(%q) = %#v; want %#v", tt.input, got, tt.want)
			}
		})
	}
}
