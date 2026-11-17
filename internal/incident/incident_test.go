package incident

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// writeFile creates path with parent directories and the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// setMtime pins the modification time of path so tests never depend on the
// clock.
func setMtime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("setting mtime of %s: %v", path, err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name        string
		text        string
		wantKind    Kind
		wantSummary string
	}{
		{
			name:        "unsupported class version",
			text:        "java.lang.UnsupportedClassVersionError: net/minecraft/server/Main",
			wantKind:    KindJava,
			wantSummary: "Java is too old for this Minecraft version",
		},
		{
			name:        "out of memory",
			text:        "java.lang.OutOfMemoryError: Java heap space",
			wantKind:    KindOOM,
			wantSummary: "The server ran out of memory",
		},
		{
			name:        "failed to bind",
			text:        "Failed to bind to port",
			wantKind:    KindPort,
			wantSummary: "The server could not bind its port",
		},
		{
			name:        "address already in use",
			text:        "java.net.BindException: Address already in use: bind",
			wantKind:    KindPort,
			wantSummary: "The server could not bind its port",
		},
		{
			name:        "missing dependency",
			text:        "Missing or unsupported mandatory dependencies: mod x needs y",
			wantKind:    KindMissingDep,
			wantSummary: "A mod dependency is missing",
		},
		{
			name:        "mixin apply failed",
			text:        "Mixin apply failed for mixin.json",
			wantKind:    KindModInit,
			wantSummary: "A mod failed to initialize",
		},
		{
			name:        "unknown",
			text:        "Exception in thread \"main\"",
			wantKind:    KindUnknown,
			wantSummary: "The server exited with an error",
		},
		{
			name:        "java wins over oom",
			text:        "java.lang.OutOfMemoryError: Java heap space\nCaused by: java.lang.UnsupportedClassVersionError",
			wantKind:    KindJava,
			wantSummary: "Java is too old for this Minecraft version",
		},
		{
			name:        "empty text",
			text:        "",
			wantKind:    KindUnknown,
			wantSummary: "The server exited with an error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, summary := Classify(tc.text)
			if kind != tc.wantKind {
				t.Fatalf("Classify kind = %q, want %q", kind, tc.wantKind)
			}
			if summary != tc.wantSummary {
				t.Fatalf("Classify summary = %q, want %q", summary, tc.wantSummary)
			}
			if summary == "" {
				t.Fatal("Classify returned an empty summary")
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		maxBytes int
		want     string
	}{
		{name: "shorter than the cap", content: "hello", maxBytes: 100, want: "hello"},
		{name: "exactly the cap", content: "hello", maxBytes: 5, want: "hello"},
		{name: "over the cap keeps the tail", content: "0123456789", maxBytes: 4, want: "6789"},
		{name: "keeps a rune whole", content: "abcd" + strings.Repeat("é", 3), maxBytes: 5, want: "éé"},
		{name: "zero cap", content: "hello", maxBytes: 0, want: ""},
		{name: "negative cap", content: "hello", maxBytes: -1, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Truncate(tc.content, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tc.content, tc.maxBytes, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Truncate(%q, %d) split a UTF-8 rune: %q", tc.content, tc.maxBytes, got)
			}
			if len(got) > tc.maxBytes && tc.maxBytes > 0 {
				t.Fatalf("Truncate(%q, %d) returned %d bytes", tc.content, tc.maxBytes, len(got))
			}
		})
	}
}

func TestLatestReportsNoEvidence(t *testing.T) {
	rep, found, err := Latest(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if found {
		t.Fatalf("Latest found evidence in an empty folder: %+v", rep)
	}
	if rep.Path != "" || rep.LogPath != "" {
		t.Fatalf("Latest reported paths without evidence: %+v", rep)
	}
}

func TestLatestUsesLogOnly(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "logs", "latest.log")
	writeFile(t, logPath, strings.Join([]string{
		"[12:00:00] [Server thread/INFO]: Starting minecraft server version 1.21.4",
		"[12:00:01] [Server thread/ERROR]: java.lang.OutOfMemoryError: Java heap space",
		"[12:00:02] [Server thread/INFO]: Stopping server   ",
		"",
		"",
	}, "\r\n"))
	when := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	setMtime(t, logPath, when)

	rep, found, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !found {
		t.Fatal("Latest did not find the log")
	}
	if rep.Path != "" {
		t.Fatalf("Path = %q, want empty without a crash report", rep.Path)
	}
	if rep.LogPath != logPath {
		t.Fatalf("LogPath = %q, want %q", rep.LogPath, logPath)
	}
	if rep.Kind != KindOOM {
		t.Fatalf("Kind = %q, want %q", rep.Kind, KindOOM)
	}
	if rep.Summary != "The server ran out of memory" {
		t.Fatalf("Summary = %q", rep.Summary)
	}
	if !rep.When.Equal(when) {
		t.Fatalf("When = %v, want %v", rep.When, when)
	}
	want := []string{
		"[12:00:00] [Server thread/INFO]: Starting minecraft server version 1.21.4",
		"[12:00:01] [Server thread/ERROR]: java.lang.OutOfMemoryError: Java heap space",
		"[12:00:02] [Server thread/INFO]: Stopping server",
	}
	if len(rep.Lines) != len(want) {
		t.Fatalf("Lines = %q, want %q", rep.Lines, want)
	}
	for i := range want {
		if rep.Lines[i] != want[i] {
			t.Fatalf("Lines[%d] = %q, want %q", i, rep.Lines[i], want[i])
		}
	}
}

func TestLatestUsesCrashReportOnly(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "crash-reports", "crash-2026-01-02.txt")
	writeFile(t, reportPath, strings.Join([]string{
		"---- Minecraft Crash Report ----",
		"Description: Exception in server tick loop",
		"",
		"Missing or unsupported mandatory dependencies:",
		"\tmod 'create' (1.0) requires 'flywheel'",
	}, "\n"))
	when := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	setMtime(t, reportPath, when)

	rep, found, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !found {
		t.Fatal("Latest did not find the crash report")
	}
	if rep.Path != reportPath {
		t.Fatalf("Path = %q, want %q", rep.Path, reportPath)
	}
	if rep.LogPath != "" {
		t.Fatalf("LogPath = %q, want empty without a log", rep.LogPath)
	}
	if rep.Kind != KindMissingDep {
		t.Fatalf("Kind = %q, want %q", rep.Kind, KindMissingDep)
	}
	if !rep.When.Equal(when) {
		t.Fatalf("When = %v, want %v", rep.When, when)
	}
	if len(rep.Lines) == 0 {
		t.Fatal("Lines is empty")
	}
}

func TestLatestPicksTheNewestCrashReport(t *testing.T) {
	dir := t.TempDir()
	crashDir := filepath.Join(dir, "crash-reports")
	if err := os.MkdirAll(filepath.Join(crashDir, "nested"), 0o755); err != nil {
		t.Fatalf("creating nested folder: %v", err)
	}
	writeFile(t, filepath.Join(crashDir, "notes.md"), "not a crash report")
	base := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		text string
		when time.Time
	}{
		"old.txt":   {"java.lang.OutOfMemoryError", base},
		"newer.txt": {"Mixin apply failed", base.Add(time.Hour)},
		"newest.txt": {
			"java.lang.UnsupportedClassVersionError",
			base.Add(2 * time.Hour),
		},
	} {
		path := filepath.Join(crashDir, name)
		writeFile(t, path, tc.text)
		setMtime(t, path, tc.when)
	}

	rep, found, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !found {
		t.Fatal("Latest did not find a crash report")
	}
	if want := filepath.Join(crashDir, "newest.txt"); rep.Path != want {
		t.Fatalf("Path = %q, want %q", rep.Path, want)
	}
	if rep.Kind != KindJava {
		t.Fatalf("Kind = %q, want %q", rep.Kind, KindJava)
	}
}

func TestLatestPrefersTheCrashReportButKeepsTheNewestTime(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "crash-reports", "crash.txt")
	writeFile(t, reportPath, "Missing or unsupported mandatory dependencies")
	whenReport := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	setMtime(t, reportPath, whenReport)

	logPath := filepath.Join(dir, "logs", "latest.log")
	writeFile(t, logPath, "java.lang.OutOfMemoryError: Java heap space")
	whenLog := whenReport.Add(time.Hour)
	setMtime(t, logPath, whenLog)

	rep, found, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !found {
		t.Fatal("Latest did not find any evidence")
	}
	if rep.Path != reportPath || rep.LogPath != logPath {
		t.Fatalf("paths = %q / %q, want both sources", rep.Path, rep.LogPath)
	}
	if rep.Kind != KindMissingDep {
		t.Fatalf("Kind = %q, want the crash report classification %q", rep.Kind, KindMissingDep)
	}
	if !rep.When.Equal(whenLog) {
		t.Fatalf("When = %v, want the newest evidence %v", rep.When, whenLog)
	}
}

func TestLatestCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Latest(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Latest error = %v, want context.Canceled", err)
	}
}

func TestLatestLogReadError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs", "latest.log"), 0o755); err != nil {
		t.Fatalf("creating log folder: %v", err)
	}
	if _, _, err := Latest(context.Background(), dir); err == nil {
		t.Fatal("Latest ignored an unreadable latest.log")
	}
}

func TestLatestCrashFolderError(t *testing.T) {
	// A NUL byte is rejected by the OS on every platform with an error that
	// is not os.ErrNotExist, which is the only portable way to make the crash
	// folder listing fail: a regular file at that path reports ErrNotExist.
	if _, _, err := Latest(context.Background(), "\x00"); err == nil {
		t.Fatal("Latest ignored an unreadable crash-reports entry")
	}
}

func TestHas(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  bool
	}{
		{
			name: "crash reports folder exists",
			setup: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, "crash-reports"), 0o755); err != nil {
					t.Fatalf("creating crash-reports: %v", err)
				}
			},
			want: true,
		},
		{
			name:  "missing crash reports folder",
			setup: func(t *testing.T, dir string) {},
			want:  false,
		},
		{
			name: "crash-reports is a regular file",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "crash-reports"), "not a folder")
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			if got := Has(dir); got != tc.want {
				t.Fatalf("Has(%q) = %v, want %v", dir, got, tc.want)
			}
		})
	}
}

func TestLatestUnknownKindKeepsTheTail(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	writeFile(t, filepath.Join(dir, "logs", "latest.log"), strings.TrimSuffix(b.String(), "\n"))

	rep, found, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !found {
		t.Fatal("Latest did not find the log")
	}
	if rep.Kind != KindUnknown {
		t.Fatalf("Kind = %q, want %q", rep.Kind, KindUnknown)
	}
	if len(rep.Lines) != maxLines {
		t.Fatalf("len(Lines) = %d, want %d", len(rep.Lines), maxLines)
	}
	if rep.Lines[0] != "line 50" {
		t.Fatalf("Lines[0] = %q, want %q", rep.Lines[0], "line 50")
	}
	if last := rep.Lines[len(rep.Lines)-1]; last != "line 249" {
		t.Fatalf("last line = %q, want %q", last, "line 249")
	}
}

func TestLatestShortUnknownLog(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "logs", "latest.log"), "hello\nworld\n")

	rep, _, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(rep.Lines) != 2 || rep.Lines[0] != "hello" || rep.Lines[1] != "world" {
		t.Fatalf("Lines = %q, want the whole short log", rep.Lines)
	}
}

func TestLatestCapsManyMatches(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "java.lang.OutOfMemoryError while loading chunk %d\n", i)
	}
	writeFile(t, filepath.Join(dir, "logs", "latest.log"), strings.TrimSuffix(b.String(), "\n"))

	rep, _, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(rep.Lines) != maxLines {
		t.Fatalf("len(Lines) = %d, want %d", len(rep.Lines), maxLines)
	}
	if last := rep.Lines[len(rep.Lines)-1]; !strings.Contains(last, "chunk 299") {
		t.Fatalf("last line = %q, want the newest match", last)
	}
}

func TestLatestClassifiesOnlyTheLogTail(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("Failed to bind to port 25565\n")
	for i := 0; i < logTailLines+100; i++ {
		fmt.Fprintf(&b, "line %d of a long run\n", i)
	}
	writeFile(t, filepath.Join(dir, "logs", "latest.log"), strings.TrimSuffix(b.String(), "\n"))

	rep, _, err := Latest(context.Background(), dir)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rep.Kind != KindUnknown {
		t.Fatalf("Kind = %q, want %q: the early port error is outside the tail", rep.Kind, KindUnknown)
	}
	if len(rep.Lines) != maxLines {
		t.Fatalf("len(Lines) = %d, want %d", len(rep.Lines), maxLines)
	}
}

func TestMtimeOfMissingFile(t *testing.T) {
	if got := mtimeOf(filepath.Join(t.TempDir(), "missing.txt")); !got.IsZero() {
		t.Fatalf("mtimeOf(missing) = %v, want the zero time", got)
	}
}

func TestUploadSuccess(t *testing.T) {
	var gotField, gotContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/log" {
			t.Errorf("path = %q, want /1/log", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parsing form: %v", err)
		}
		gotField = r.PostForm.Get("content")
		_, hasContent := r.PostForm["content"]
		if !hasContent {
			t.Error(`the form field is not named "content"`)
		}
		gotContent = r.PostFormValue("content")
		_, _ = io.WriteString(w, `{"success":true,"url":"https://api.mclo.gs/abc123"}`)
	}))
	defer srv.Close()

	got, err := Upload(context.Background(), srv.Client(), srv.URL+"/", "server crashed")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got != "https://api.mclo.gs/abc123" {
		t.Fatalf("Upload = %q", got)
	}
	if gotField != "server crashed" || gotContent != "server crashed" {
		t.Fatalf("form content = %q, want the uploaded text", gotField)
	}
}

func TestUploadDefaultBaseURL(t *testing.T) {
	var gotURL string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return jsonResponse(200, `{"success":true,"url":"https://api.mclo.gs/x"}`), nil
	})}
	if _, err := Upload(context.Background(), client, "", "text"); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if want := "https://api.mclo.gs/1/log"; gotURL != want {
		t.Fatalf("URL = %q, want %q", gotURL, want)
	}
}

func TestUploadAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":false,"error":"Content is too large"}`)
	}))
	defer srv.Close()

	_, err := Upload(context.Background(), srv.Client(), srv.URL, "text")
	if err == nil || !strings.Contains(err.Error(), "Content is too large") {
		t.Fatalf("Upload error = %v, want the service message", err)
	}
}

func TestUploadAPIErrorWithoutMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"success":false}`)
	}))
	defer srv.Close()

	if _, err := Upload(context.Background(), srv.Client(), srv.URL, "text"); err == nil {
		t.Fatal("Upload accepted a rejected paste without a message")
	}
}

func TestUploadInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()

	if _, err := Upload(context.Background(), srv.Client(), srv.URL, "text"); err == nil {
		t.Fatal("Upload accepted an unparsable answer")
	}
}

func TestUploadNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := Upload(context.Background(), srv.Client(), srv.URL, "text"); err == nil {
		t.Fatal("Upload accepted an error status")
	}
}

func TestUploadInvalidBaseURL(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("the request must not be sent with an invalid URL")
		return nil, nil
	})}
	if _, err := Upload(context.Background(), client, "http://[::1", "text"); err == nil {
		t.Fatal("Upload accepted an invalid base URL")
	}
}

func TestUploadTransportError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})}
	if _, err := Upload(context.Background(), client, "https://example.invalid", "text"); err == nil {
		t.Fatal("Upload ignored a transport error")
	}
}

func TestUploadReadError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		resp := jsonResponse(200, "ignored")
		resp.Body = errReader{}
		return resp, nil
	})}
	if _, err := Upload(context.Background(), client, "https://example.invalid", "text"); err == nil {
		t.Fatal("Upload ignored a read error")
	}
}

func TestUploadTruncatesLargeContent(t *testing.T) {
	var gotLen int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parsing form: %v", err)
		}
		gotLen = len(r.PostFormValue("content"))
		return jsonResponse(200, `{"success":true,"url":"https://api.mclo.gs/x"}`), nil
	})}
	content := strings.Repeat("a", MaxUploadBytes+1024)
	if _, err := Upload(context.Background(), client, "https://example.invalid", content); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if gotLen != MaxUploadBytes {
		t.Fatalf("uploaded %d bytes, want the %d byte cap", gotLen, MaxUploadBytes)
	}
}

// roundTripFunc lets a test answer HTTP without a network listener.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// jsonResponse builds a canned HTTP response with a JSON body.
func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// errReader fails every read so the response-body error path is reachable.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("body failed") }
func (errReader) Close() error             { return nil }
