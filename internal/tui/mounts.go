package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lgldsilva/ai-launcher/internal/config"
)

func (m *Model) updateMountInput(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case keyEscape:
		m.inputActive = false
		m.status = "Mount addition cancelled"
	case keyEnter:
		m.commitMountInput()
	case "tab":
		// Tab completes directory names (shell-style). Mode toggle is Ctrl+T.
		m.completeMountPath()
	case "ctrl+t":
		m.toggleMountBrowserMode()
	case "right":
		// Arrows always drive the browser, even while a path is being typed.
		m.enterSelectedMountDir()
	case "left":
		m.mountBrowserUp()
	case keyUp:
		m.moveMountCursor(-1)
	case keyDown:
		m.moveMountCursor(1)
	case "l", "h", keyUpLetter, keyDownLetter:
		m.handleMountNavKey(key)
	case keyBackspace:
		m.handleMountBackspace()
	default:
		if key.Type == tea.KeyRunes {
			m.appendMountInput(string(key.Runes))
		}
	}
	return *m, nil
}

// commitMountInput adds a mount on Enter: when the input is empty or only
// reflects the current browser directory (after →/l navigation), add the
// highlighted entry; otherwise add the typed path as-is.
func (m *Model) commitMountInput() {
	path := strings.TrimSpace(m.mountInput)
	if m.shouldAddBrowserSelection(path) {
		path = m.selectedMountPath()
	}
	m.addMount(path)
}

// toggleMountBrowserMode flips the ro/rw mode of the mount being added.
func (m *Model) toggleMountBrowserMode() {
	if m.mountMode == modeReadOnlyID {
		m.mountMode = modeReadWriteID
	} else {
		m.mountMode = modeReadOnlyID
	}
	m.status = "Mount mode: " + m.mountMode
}

// handleMountNavKey handles the j/k/l/h keys in the mount browser: while
// typing a path, letters (including j/k/l/h) go into the input; when browsing
// with an empty input they navigate the directory list.
func (m *Model) handleMountNavKey(key tea.KeyMsg) {
	if m.mountTyped || key.Type == tea.KeyRunes && m.mountInput != "" {
		if key.Type == tea.KeyRunes {
			m.appendMountInput(string(key.Runes))
		}
		return
	}
	switch key.String() {
	case "l":
		m.enterSelectedMountDir()
	case "h":
		m.mountBrowserUp()
	case keyUpLetter:
		m.moveMountCursor(-1)
	case keyDownLetter:
		m.moveMountCursor(1)
	}
}

// handleMountBackspace deletes one rune from the path input and re-syncs the
// browser (full refresh when the input becomes empty).
func (m *Model) handleMountBackspace() {
	if m.mountInput != "" {
		runes := []rune(m.mountInput)
		m.mountInput = string(runes[:len(runes)-1])
	}
	if strings.TrimSpace(m.mountInput) == "" {
		m.mountTyped = false
		m.mountFilter = ""
		m.refreshMountEntries()
	} else {
		m.syncMountBrowserFromInput()
	}
}

func (m *Model) appendMountInput(text string) {
	m.mountInput += text
	m.mountTyped = true
	m.syncMountBrowserFromInput()
}

// shouldAddBrowserSelection reports whether Enter should commit the
// highlighted browser row instead of the raw path input.
func (m Model) shouldAddBrowserSelection(input string) bool {
	if len(m.mountEntries) == 0 {
		return false
	}
	if strings.TrimSpace(input) == "" {
		return true
	}
	cleaned := filepath.Clean(expandMountPath(input))
	return cleaned == filepath.Clean(m.mountDir)
}

func (m *Model) moveMountCursor(delta int) {
	if len(m.mountEntries) == 0 {
		return
	}
	m.mountCursor = (m.mountCursor + delta + len(m.mountEntries)) % len(m.mountEntries)
}

// enterSelectedMountDir opens the highlighted browser row: ".." goes up,
// directories become the new browser root and are written into the path input
// with a trailing separator so further typing continues from there.
func (m *Model) enterSelectedMountDir() {
	if len(m.mountEntries) == 0 {
		return
	}
	name := m.mountEntries[m.mountCursor]
	if name == ".." {
		m.mountBrowserUp()
		return
	}
	path := filepath.Join(m.mountDir, name)
	if !isDirectory(path) {
		m.status = "Not a directory: " + path
		return
	}
	m.mountDir = path
	m.mountInput = pathWithTrailingSep(path)
	m.mountTyped = true
	m.mountFilter = ""
	m.refreshMountEntries()
	m.status = "Directory opened · Tab completes · Enter adds · Ctrl+T toggles ro/rw"
}

func (m *Model) mountBrowserUp() {
	parent := filepath.Dir(m.mountDir)
	if parent == m.mountDir {
		return
	}
	m.mountDir = parent
	if m.mountTyped || m.mountInput != "" {
		m.mountInput = pathWithTrailingSep(parent)
		m.mountTyped = true
	}
	m.mountFilter = ""
	m.refreshMountEntries()
	m.status = "Parent directory opened"
}

func (m *Model) startMountBrowser() {
	m.inputActive = true
	m.mountInput = ""
	m.mountTyped = false
	m.mountFilter = ""
	m.mountMode = modeReadWriteID
	m.mountDir = mustWorkingDirectory()
	m.refreshMountEntries()
	m.status = "Pick a folder below (or type a path). Enter adds it. Esc cancels."
}

// syncMountBrowserFromInput re-points the browser at the directory implied by
// the typed path and filters entries by the unfinished basename prefix.
func (m *Model) syncMountBrowserFromInput() {
	dir, prefix := m.inputDirAndPrefix()
	if dir != "" {
		m.mountDir = dir
	}
	m.mountFilter = prefix
	m.refreshMountEntries()
}

// completeMountPath is shell-style Tab completion against directories under the
// path currently being typed (or under the browser root when the input is empty).
func (m *Model) completeMountPath() {
	dir, prefix := m.inputDirAndPrefix()
	matches := directoryMatches(dir, prefix)
	if len(matches) == 0 {
		m.status = "No directory matches for " + quotePrefix(prefix)
		return
	}
	if len(matches) == 1 {
		m.applyMountCompletion(dir, matches[0])
		return
	}
	lcp := longestCommonPrefix(matches)
	// Extend when the shared prefix is longer, or when it only differs by case
	// from what the user typed (macOS/Windows case-insensitive volumes).
	if len(lcp) > len(prefix) || (lcp != prefix && strings.EqualFold(lcp, prefix)) {
		m.mountInput = joinMountInput(dir, lcp)
		m.mountTyped = true
		m.syncMountBrowserFromInput()
		m.status = fmt.Sprintf("%d matches — Tab again to cycle", len(matches))
		return
	}
	// Cycle through matches, putting each full path into the input.
	if len(m.mountEntries) == 0 {
		return
	}
	// Advance past ".." if present so cycling stays on real matches.
	for range len(m.mountEntries) {
		m.mountCursor = (m.mountCursor + 1) % len(m.mountEntries)
		if m.mountEntries[m.mountCursor] != ".." {
			break
		}
	}
	name := m.mountEntries[m.mountCursor]
	if name == ".." {
		return
	}
	full := filepath.Join(dir, name)
	if isDirectory(full) {
		m.mountInput = pathWithTrailingSep(full)
	} else {
		m.mountInput = full
	}
	m.mountTyped = true
	m.syncMountBrowserFromInput()
	m.status = fmt.Sprintf("Match %d/%d: %s", indexOfMatch(matches, name)+1, len(matches), name)
}

func (m *Model) applyMountCompletion(dir, name string) {
	full := filepath.Join(dir, name)
	if isDirectory(full) {
		m.mountDir = full
		m.mountInput = pathWithTrailingSep(full)
		m.mountFilter = ""
		m.mountTyped = true
		m.refreshMountEntries()
		m.status = "Completed: " + full
		return
	}
	m.mountInput = full
	m.mountTyped = true
	m.syncMountBrowserFromInput()
	m.status = "Completed: " + full
}

// inputDirAndPrefix splits the typed path into the directory to list and the
// unfinished basename used as a completion/filter prefix.
func (m Model) inputDirAndPrefix() (dir, prefix string) {
	raw := m.mountInput
	if strings.TrimSpace(raw) == "" {
		return m.mountDir, ""
	}
	expanded := expandMountPath(raw)
	trailing := strings.HasSuffix(raw, "/") || strings.HasSuffix(raw, string(filepath.Separator))
	if !filepath.IsAbs(expanded) {
		base := m.mountDir
		if base == "" {
			base = mustWorkingDirectory()
		}
		expanded = filepath.Join(base, expanded)
	}
	if trailing {
		return filepath.Clean(expanded), ""
	}
	return filepath.Dir(expanded), filepath.Base(expanded)
}

func (m *Model) refreshMountEntries() {
	previous := m.currentMountEntry()
	entries, err := os.ReadDir(m.mountDir)
	if err != nil {
		m.mountEntries = nil
		m.mountCursor = 0
		return
	}
	m.mountEntries = m.mountEntries[:0]
	if m.mountFilter == "" {
		if parent := filepath.Dir(m.mountDir); parent != m.mountDir {
			m.mountEntries = append(m.mountEntries, "..")
		}
	}
	for _, entry := range entries {
		if m.mountEntryVisible(entry) {
			m.mountEntries = append(m.mountEntries, entry.Name())
		}
	}
	sort.Strings(m.mountEntries)
	m.mountCursor = 0
	if previous != "" {
		m.mountCursor = indexOfMatch(m.mountEntries, previous)
	}
}

// currentMountEntry returns the entry name under the browser cursor, or ""
// when the cursor is out of range.
func (m Model) currentMountEntry() string {
	if m.mountCursor < len(m.mountEntries) {
		return m.mountEntries[m.mountCursor]
	}
	return ""
}

// mountEntryVisible reports whether a directory entry survives the typed
// basename filter and is (or links to) a directory.
func (m Model) mountEntryVisible(entry os.DirEntry) bool {
	name := entry.Name()
	if m.mountFilter != "" && !hasPathPrefixFold(name, m.mountFilter) {
		return false
	}
	return entryIsDir(m.mountDir, entry)
}

func (m Model) selectedMountPath() string {
	if m.mountCursor >= len(m.mountEntries) {
		return ""
	}
	name := m.mountEntries[m.mountCursor]
	if name == ".." {
		return filepath.Dir(m.mountDir)
	}
	return filepath.Join(m.mountDir, name)
}

func (m *Model) addMount(path string) {
	path = expandMountPath(strings.TrimSpace(path))
	if path == "" {
		m.status = "Mount path cannot be empty"
		return
	}
	path = filepath.Clean(path)
	for _, mount := range m.launch.Mounts {
		if mount.Path == path {
			m.inputActive = false
			m.status = "Path is already in the mount list"
			return
		}
	}
	m.launch.Mounts = append(m.launch.Mounts, config.Mount{Path: path, Mode: m.mountMode})
	m.cursor = len(m.launch.Mounts) - 1
	m.inputActive = false
	m.mountTyped = false
	m.mountFilter = ""
	modeLabel := "read-write"
	if m.mountMode == modeReadOnlyID {
		modeLabel = modeReadOnly
	}
	m.status = "Added " + path + " (" + modeLabel + "). r = RUN · a = add another · Space = ro/rw · Backspace = remove"
}

func mustWorkingDirectory() string {
	path, err := os.Getwd()
	if err != nil {
		return "."
	}
	return path
}

// expandMountPath expands a leading ~ to the user home directory.
func expandMountPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func pathWithTrailingSep(path string) string {
	if path == "" {
		return string(filepath.Separator)
	}
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return path
	}
	return path + string(filepath.Separator)
}

func joinMountInput(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return filepath.Join(dir, name)
}

func directoryMatches(dir, prefix string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	matches := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if prefix != "" && !hasPathPrefixFold(name, prefix) {
			continue
		}
		if !entryIsDir(dir, entry) {
			continue
		}
		matches = append(matches, name)
	}
	sort.Strings(matches)
	return matches
}

func entryIsDir(parent string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	// Symlinks to directories report as non-dir via DirEntry.IsDir(); Stat.
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	return isDirectory(filepath.Join(parent, entry.Name()))
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func hasPathPrefixFold(name, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
}

func longestCommonPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}
	prefix := values[0]
	for _, value := range values[1:] {
		for !strings.HasPrefix(value, prefix) {
			if prefix == "" {
				return ""
			}
			prefix = prefix[:len(prefix)-1]
		}
	}
	return prefix
}

func indexOfMatch(matches []string, name string) int {
	for i, match := range matches {
		if match == name {
			return i
		}
	}
	return 0
}

func quotePrefix(prefix string) string {
	if prefix == "" {
		return "(any)"
	}
	return strconv.Quote(prefix)
}

func (m *Model) removeMount() {
	if m.section != 2 || len(m.launch.Mounts) == 0 || m.cursor >= len(m.launch.Mounts) {
		return
	}
	removed := m.launch.Mounts[m.cursor].Path
	m.launch.Mounts = append(m.launch.Mounts[:m.cursor], m.launch.Mounts[m.cursor+1:]...)
	if m.cursor >= len(m.launch.Mounts) {
		m.cursor = len(m.launch.Mounts) - 1
	}
	m.status = "Mount removed: " + removed
}
