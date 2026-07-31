package launcher

import "testing"

func TestSanitizeDisplayEscapesTerminalControls(t *testing.T) {
	in := "ok\x1b[31mRED\x07bell\tkeep\nline"
	got := SanitizeDisplay(in)
	want := "ok\\x1b[31mRED\\x07bell\tkeep\nline"
	if got != want {
		t.Fatalf("SanitizeDisplay() = %q; want %q", got, want)
	}
	if SanitizeDisplay("") != "" {
		t.Fatal("empty stays empty")
	}
	if SanitizeDisplay("plain-path") != "plain-path" {
		t.Fatal("plain text must pass through")
	}
}
