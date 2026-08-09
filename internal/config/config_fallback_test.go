package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The base-directory fallback chain in LocalConfigDir and
// LegacyLocalConfigPath (config.go:26-48) is a mutation hotspot: every
// comparison (`len(projectDir) > 0`, `base == ""`), the first-element
// selection, and the `"."` fallback below are pinned so boundary and negation
// mutants cannot survive.

func TestLocalConfigDirResolvesTheBaseDirectory(t *testing.T) {
	project := t.TempDir()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "explicit project directory",
			args: []string{project},
			want: filepath.Join(project, ".ai-launcher"),
		},
		{
			name: "first argument wins over extras",
			args: []string{project, filepath.Join(project, "ignored")},
			want: filepath.Join(project, ".ai-launcher"),
		},
		{
			name: "relative project directory is kept relative",
			args: []string{filepath.Join("rel", "project")},
			want: filepath.Join("rel", "project", ".ai-launcher"),
		},
		{
			name: "empty argument falls back to the current directory marker",
			args: []string{""},
			want: filepath.Join(".", ".ai-launcher"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocalConfigDir(tt.args...); got != tt.want {
				t.Fatalf("LocalConfigDir(%q) = %q; want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestLocalConfigPathJoinsTheConfigFileName(t *testing.T) {
	project := t.TempDir()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "explicit project directory",
			args: []string{project},
			want: filepath.Join(project, ".ai-launcher", "config.yaml"),
		},
		{
			name: "empty argument falls back to the current directory marker",
			args: []string{""},
			want: filepath.Join(".", ".ai-launcher", "config.yaml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocalConfigPath(tt.args...); got != tt.want {
				t.Fatalf("LocalConfigPath(%q) = %q; want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestLegacyLocalConfigPathResolvesTheBaseDirectory(t *testing.T) {
	project := t.TempDir()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "explicit project directory",
			args: []string{project},
			want: filepath.Join(project, ".ai-launch.yaml"),
		},
		{
			name: "first argument wins over extras",
			args: []string{project, filepath.Join(project, "ignored")},
			want: filepath.Join(project, ".ai-launch.yaml"),
		},
		{
			name: "relative project directory is kept relative",
			args: []string{filepath.Join("rel", "project")},
			want: filepath.Join("rel", "project", ".ai-launch.yaml"),
		},
		{
			name: "empty argument falls back to the current directory marker",
			args: []string{""},
			want: filepath.Join(".", ".ai-launch.yaml"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LegacyLocalConfigPath(tt.args...); got != tt.want {
				t.Fatalf("LegacyLocalConfigPath(%q) = %q; want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestLocalConfigPathsWithoutArgumentUseTheWorkingDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"directory", LocalConfigDir(), filepath.Join(wd, ".ai-launcher")},
		{"new path", LocalConfigPath(), filepath.Join(wd, ".ai-launcher", "config.yaml")},
		{"legacy path", LegacyLocalConfigPath(), filepath.Join(wd, ".ai-launch.yaml")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("path without arguments = %q; want %q", tt.got, tt.want)
			}
			if !filepath.IsAbs(tt.got) {
				t.Fatalf("path without arguments = %q; want an absolute working-directory path", tt.got)
			}
		})
	}
}
