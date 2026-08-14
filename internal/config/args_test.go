package config

import (
	"reflect"
	"testing"
)

func TestSplitArgsParsesShellStyleTokens(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "bare words",
			input: "--model sonnet --verbose",
			want:  []string{"--model", "sonnet", "--verbose"},
		},
		{
			name:  "double quotes preserve spaces",
			input: `--model "claude 3.5" --verbose`,
			want:  []string{"--model", "claude 3.5", "--verbose"},
		},
		{
			name:  "single quotes preserve spaces",
			input: `--message 'hello world'`,
			want:  []string{"--message", "hello world"},
		},
		{
			name:  "mixed quotes",
			input: `'single quoted' "double quoted"`,
			want:  []string{"single quoted", "double quoted"},
		},
		{
			name:  "escaped double quote",
			input: `--message "say \"hi\""`,
			want:  []string{"--message", `say "hi"`},
		},
		{
			name:  "escaped backslash in double quotes",
			input: `--path "C:\\Users\\dev"`,
			want:  []string{"--path", `C:\Users\dev`},
		},
		{
			name:  "backslash is literal inside single quotes",
			input: `--path 'C:\Users\dev'`,
			want:  []string{"--path", `C:\Users\dev`},
		},
		{
			name:  "escaped space outside quotes",
			input: `--file foo\ bar.txt`,
			want:  []string{"--file", "foo bar.txt"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace only",
			input: "   \n\t  ",
			want:  nil,
		},
		{
			name:    "unterminated double quote",
			input:   `--model "claude`,
			wantErr: true,
		},
		{
			name:    "unterminated single quote",
			input:   `--model 'claude`,
			wantErr: true,
		},
		{
			name:    "trailing escape",
			input:   `--model claude\`,
			wantErr: true,
		},
		{
			name:  "quoted empty string",
			input: `--flag ""`,
			want:  []string{"--flag", ""},
		},
		{
			name:  "adjacent quoted tokens concatenate",
			input: `"a""b"`,
			want:  []string{"ab"},
		},
		{
			name:  "quote in middle of bare word concatenates",
			input: `foo"bar baz"`,
			want:  []string{"foobar baz"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitArgs(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SplitArgs(%q) = %v; want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitArgs(%q) error = %v", tc.input, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SplitArgs(%q) = %#v; want %#v", tc.input, got, tc.want)
			}
		})
	}
}
