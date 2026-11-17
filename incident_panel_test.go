// incident_panel_test.go: tests for the incident panel.
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/incident"
)

// waitIncIdle waits until the panel background work finishes.
func waitIncIdle(t *testing.T, a *app) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for a.incBusy.Get() {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for the incident panel")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIncidentContentPath(t *testing.T) {
	cases := []struct {
		name   string
		report incident.Report
		want   string
	}{
		{
			name:   "crash report wins over the log",
			report: incident.Report{Path: "/inst/crash-reports/crash.txt", LogPath: "/inst/logs/latest.log"},
			want:   "/inst/crash-reports/crash.txt",
		},
		{
			name:   "log only",
			report: incident.Report{LogPath: "/inst/logs/latest.log"},
			want:   "/inst/logs/latest.log",
		},
		{
			name:   "no evidence",
			report: incident.Report{},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := incidentContentPath(tc.report); got != tc.want {
				t.Fatalf("incidentContentPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIncidentPanelLoadsAndUploads walks the panel contract: the evidence is
// loaded on open, the upload waits for the confirmation and the URL lands in
// the panel. The httptest server stands in for mclo.gs.
func TestIncidentPanelLoadsAndUploads(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "logs", "latest.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("java.lang.OutOfMemoryError: Java heap space\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := config.Instance{Name: "alpha", Dir: dir, Version: ""}
	a := newTestApp(t, inst)

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/log" {
			t.Errorf("upload path = %q, want /1/log", r.URL.Path)
		}
		_ = r.ParseForm()
		got = r.PostFormValue("content")
		_, _ = w.Write([]byte(`{"success":true,"url":"https://mclo.gs/abc"}`))
	}))
	defer srv.Close()
	a.incClient = srv.Client()
	a.incBaseURL = srv.URL

	a.incOpenPanel()
	waitIncIdle(t, a)

	if !a.incOpen.Get() {
		t.Fatal("the incident panel did not open")
	}
	if !a.incFound.Get() {
		t.Fatal("the incident panel did not find the log")
	}
	if got := a.incReport.Get().Summary; got != "The server ran out of memory" {
		t.Fatalf("Summary = %q", got)
	}

	a.incAskUpload()
	if !a.incConfirm.Get() {
		t.Fatal("u did not ask for confirmation before uploading")
	}
	if got != "" {
		t.Fatal("the log was uploaded before the confirmation")
	}

	a.incUpload()
	waitIncIdle(t, a)

	if !strings.Contains(got, "OutOfMemoryError") {
		t.Fatalf("uploaded content = %q, want the log", got)
	}
	if want := "https://mclo.gs/abc"; a.incURL.Get() != want {
		t.Fatalf("uploaded URL = %q, want %q", a.incURL.Get(), want)
	}

	a.incClose()
	if a.incOpen.Get() || a.incURL.Get() != "" || a.incFound.Get() {
		t.Fatal("closing the panel must reset its state")
	}
}
