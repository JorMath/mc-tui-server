// Package incident explains why a Minecraft instance crashed and uploads
// the evidence to a public paste service.
package incident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxUploadBytes is the cap applied before uploading (512 KB).
const MaxUploadBytes = 512 * 1024

const (
	// crashReportsDir holds the reports the server writes when it crashes.
	crashReportsDir = "crash-reports"
	// latestLog is the log of the current (or last) run.
	latestLog = "logs/latest.log"
	// maxLines bounds the excerpt stored in a Report.
	maxLines = 200
	// logTailLines is how much of a log is inspected: the crash reason is at
	// the end, and an early recoverable error must not win the classification.
	logTailLines = 2000
	// contextLines is how many lines surround each matched line.
	contextLines = 2
	// defaultPasteURL is the public paste service used when none is given.
	defaultPasteURL = "https://api.mclo.gs"
)

// Kind classifies the most likely cause of a crash.
type Kind string

const (
	KindOOM        Kind = "oom"
	KindModInit    Kind = "mod-init"
	KindMissingDep Kind = "missing-dep"
	KindPort       Kind = "port"
	KindJava       Kind = "java"
	KindUnknown    Kind = "unknown"
)

// Report is the evidence collected for the most recent crash.
type Report struct {
	Kind    Kind      // classification of the most likely cause
	Summary string    // one human-readable line, no trailing period
	Path    string    // crash report file, "" when there is none
	LogPath string    // logs/latest.log when it exists, "" otherwise
	Lines   []string  // bounded excerpt, at most 200 lines
	When    time.Time // modification time of the newest evidence
}

// rule maps the marker of a crash cause to its classification. The order of
// rules is the precedence order used by Classify.
type rule struct {
	kind    Kind
	summary string
	markers []string
}

// rules is ordered: an UnsupportedClassVersionError often shows up inside an
// OOM-adjacent startup failure, so Java is checked before memory.
var rules = []rule{
	{
		kind:    KindJava,
		summary: "Java is too old for this Minecraft version",
		markers: []string{"UnsupportedClassVersionError"},
	},
	{
		kind:    KindOOM,
		summary: "The server ran out of memory",
		markers: []string{"OutOfMemoryError"},
	},
	{
		kind:    KindPort,
		summary: "The server could not bind its port",
		markers: []string{"Failed to bind to port", "Address already in use"},
	},
	{
		kind:    KindMissingDep,
		summary: "A mod dependency is missing",
		markers: []string{"Missing or unsupported mandatory dependencies"},
	},
	{
		kind:    KindModInit,
		summary: "A mod failed to initialize",
		markers: []string{"Mixin apply failed"},
	},
}

// Classify inspects text and returns the most likely cause plus a one-line
// summary. It never returns an empty Summary.
func Classify(text string) (Kind, string) {
	for _, r := range rules {
		for _, marker := range r.markers {
			if strings.Contains(text, marker) {
				return r.kind, r.summary
			}
		}
	}
	return KindUnknown, "The server exited with an error"
}

// Latest returns the most recent crash evidence for the instance rooted at
// instDir. found is false when there is neither a crash report nor a log.
func Latest(ctx context.Context, instDir string) (Report, bool, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, false, err
	}
	crashPath, err := newestCrashReport(instDir)
	if err != nil {
		return Report{}, false, err
	}
	logPath := filepath.Join(instDir, filepath.FromSlash(latestLog))

	// The crash report is the primary source; the log is the fallback.
	type source struct {
		path  string
		text  string
		found bool
	}
	sources := []source{{path: crashPath}, {path: logPath}}
	for i := range sources {
		text, found, err := readText(sources[i].path)
		if err != nil {
			return Report{}, false, fmt.Errorf("reading %s: %w", sources[i].path, err)
		}
		sources[i].text, sources[i].found = text, found
	}
	crash, log := sources[0], sources[1]
	if !crash.found && !log.found {
		return Report{}, false, nil
	}

	var rep Report
	if crash.found {
		rep.Path = crash.path
	}
	if log.found {
		rep.LogPath = log.path
	}
	for _, src := range sources {
		if mod := mtimeOf(src.path); src.found && mod.After(rep.When) {
			rep.When = mod
		}
	}
	text := log.text
	if crash.found {
		text = crash.text
	} else {
		text = logTail(text)
	}
	rep.Kind, rep.Summary = Classify(text)
	rep.Lines = excerpt(strings.Split(text, "\n"), rep.Kind)
	return rep, true, nil
}

// Has reports whether instDir holds a crash-reports directory. It is the
// cheap marker the sidebar caches to point at crash evidence; it does not
// read any file.
func Has(instDir string) bool {
	info, err := os.Stat(filepath.Join(instDir, crashReportsDir))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// newestCrashReport returns the newest crash report file of the instance, or
// "" when there is none.
func newestCrashReport(instDir string) (string, error) {
	dir := filepath.Join(instDir, crashReportsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading crash reports: %w", err)
	}
	var (
		newest     string
		newestTime time.Time
	)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		mod := mtimeOf(path)
		if newest == "" || mod.After(newestTime) {
			newest, newestTime = path, mod
		}
	}
	return newest, nil
}

// readText reads a file. An empty path or a missing file reports no content
// and no error, so callers can treat optional evidence uniformly.
func readText(path string) (string, bool, error) {
	if path == "" {
		return "", false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// mtimeOf returns the modification time of path, or the zero time when it
// cannot be read.
func mtimeOf(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// logTail keeps the last logTailLines lines of a log.
func logTail(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= logTailLines {
		return text
	}
	return strings.Join(lines[len(lines)-logTailLines:], "\n")
}

// excerpt selects the lines that explain a crash of the given kind: the
// matched lines plus context, or the tail of the text when nothing matched.
func excerpt(lines []string, kind Kind) []string {
	trimmed := make([]string, len(lines))
	for i, line := range lines {
		trimmed[i] = strings.TrimRight(line, " \t\r")
	}
	if markers := markersFor(kind); len(markers) > 0 {
		var (
			picked []int
			seen   = make(map[int]bool)
		)
		for i, line := range trimmed {
			if !containsAny(line, markers) {
				continue
			}
			for j := i - contextLines; j <= i+contextLines; j++ {
				if j < 0 || j >= len(trimmed) || seen[j] {
					continue
				}
				seen[j] = true
				picked = append(picked, j)
			}
		}
		if len(picked) > 0 {
			sort.Ints(picked)
			if len(picked) > maxLines {
				picked = picked[len(picked)-maxLines:]
			}
			out := make([]string, 0, len(picked))
			for _, i := range picked {
				out = append(out, trimmed[i])
			}
			return dropTrailingEmpty(out)
		}
	}
	start := len(trimmed) - maxLines
	if start < 0 {
		start = 0
	}
	return dropTrailingEmpty(append([]string(nil), trimmed[start:]...))
}

// markersFor returns the markers that classify a kind, or nil for the kinds
// that have no marker of their own.
func markersFor(kind Kind) []string {
	for _, r := range rules {
		if r.kind == kind {
			return r.markers
		}
	}
	return nil
}

// containsAny reports whether line contains one of the markers.
func containsAny(line string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// dropTrailingEmpty removes the empty lines at the end of lines.
func dropTrailingEmpty(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Upload posts content to the log paste service rooted at baseURL and
// returns the public URL it answers with.
func Upload(ctx context.Context, client *http.Client, baseURL, content string) (string, error) {
	if baseURL == "" {
		baseURL = defaultPasteURL
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/1/log"
	form := url.Values{"content": {Truncate(content, MaxUploadBytes)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building the paste request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("posting to the paste service: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading the paste response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("the paste service answered %s", resp.Status)
	}
	var answer struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", fmt.Errorf("parsing the paste response: %w", err)
	}
	if !answer.Success {
		if answer.Error != "" {
			return "", errors.New(answer.Error)
		}
		return "", errors.New("the paste service rejected the upload")
	}
	return answer.URL, nil
}

// Truncate cuts content to at most maxBytes, keeping the tail: the end of a
// Minecraft log says why the server died. It must not split a UTF-8 rune.
func Truncate(content string, maxBytes int) string {
	if len(content) <= maxBytes {
		return content
	}
	if maxBytes <= 0 {
		return ""
	}
	start := len(content) - maxBytes
	for start < len(content) && !utf8.RuneStart(content[start]) {
		start++
	}
	return content[start:]
}
