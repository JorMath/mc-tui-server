package hangar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JorMath/mc-tui-server/internal/config"
)

func ctx() context.Context { return context.Background() }

func TestDefaultBase(t *testing.T) {
	c := &Client{}
	if got := c.base(); got != "https://hangar.papermc.io" {
		t.Fatalf("base() = %q", got)
	}
	c.BaseURL = "http://x/"
	if got := c.base(); got != "http://x" {
		t.Fatalf("base() con BaseURL = %q", got)
	}
}

func TestPlatform(t *testing.T) {
	for _, typ := range []config.ServerType{config.Paper, config.Purpur} {
		p, ok := Platform(typ)
		if !ok || p != "PAPER" {
			t.Fatalf("Platform(%s) = %q,%v", typ, p, ok)
		}
	}
	for _, typ := range []config.ServerType{config.Vanilla, config.Fabric, config.Forge} {
		if p, ok := Platform(typ); ok || p != "" {
			t.Fatalf("Platform(%s) = %q,%v, want no platform", typ, p, ok)
		}
	}
}

func serve(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchParsesAndBuildsQuery(t *testing.T) {
	var gotQuery, gotPlatform, gotLimit, gotOffset string
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotQuery = r.URL.Query().Get("query")
		gotPlatform = r.URL.Query().Get("platform")
		gotLimit = r.URL.Query().Get("limit")
		gotOffset = r.URL.Query().Get("offset")
		fmt.Fprint(w, `{"pagination":{"count":2,"limit":25,"offset":0},"result":[
			{"name":"ViaVersion","namespace":{"owner":"ViaVersion","slug":"ViaVersion"},
			 "description":"Protocol support","stats":{"downloads":531399}},
			{"name":"EssentialsX","namespace":{"owner":"EssentialsX","slug":"EssentialsX"},
			 "description":"Core commands","stats":{"downloads":9000}}
		]}`)
	})
	c := &Client{BaseURL: srv.URL}
	projects, err := c.Search(ctx(), "via", "PAPER")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "via" || gotPlatform != "PAPER" || gotLimit != "25" || gotOffset != "0" {
		t.Fatalf("query params = %q/%q/%q/%q", gotQuery, gotPlatform, gotLimit, gotOffset)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %+v", projects)
	}
	first := projects[0]
	if first.Name != "ViaVersion" || first.Owner != "ViaVersion" || first.Slug != "ViaVersion" ||
		first.Description != "Protocol support" || first.Downloads != 531399 {
		t.Fatalf("first project = %+v", first)
	}
}

func TestSearchOmitsEmptyPlatform(t *testing.T) {
	var gotPlatform string
	var present bool
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPlatform = r.URL.Query().Get("platform")
		_, present = r.URL.Query()["platform"]
		fmt.Fprint(w, `{"result":[]}`)
	})
	c := &Client{BaseURL: srv.URL}
	if _, err := c.Search(ctx(), "x", ""); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if present || gotPlatform != "" {
		t.Fatalf("platform param should be omitted, got %q", gotPlatform)
	}
}

func TestSearchHTTPErrorIncludesBody(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid platform"}`)
	})
	c := &Client{BaseURL: srv.URL}
	_, err := c.Search(ctx(), "x", "PAPER")
	if err == nil {
		t.Fatal("Search should fail on a non-2xx status")
	}
	msg := err.Error()
	if !strings.Contains(msg, "HTTP 400") || !strings.Contains(msg, "invalid platform") {
		t.Fatalf("error = %q", msg)
	}
}

func TestSearchHTTPErrorWithEmptyBody(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	c := &Client{BaseURL: srv.URL}
	_, err := c.Search(ctx(), "x", "PAPER")
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("error = %v", err)
	}
}

func TestSearchErrorPropagates(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	if _, err := c.Search(ctx(), "x", "PAPER"); err == nil {
		t.Fatal("Search against a missing server should fail")
	}
}

func TestSearchMalformedJSON(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":[`)
	})
	c := &Client{BaseURL: srv.URL}
	if _, err := c.Search(ctx(), "x", "PAPER"); err == nil {
		t.Fatal("Search should fail on malformed JSON")
	}
}

const versionBody = `{
	"pagination":{"count":2,"limit":25,"offset":0},
	"result":[
		{"name":"5.12.2","channel":{"name":"Release"},
		 "downloads":{"PAPER":{"fileInfo":{"name":"ViaVersion-5.12.2.jar","sizeBytes":6519553,
		   "sha256Hash":"5D0EAA"},"externalUrl":null,
		   "downloadUrl":"https://hangarcdn.papermc.io/plugins/ViaVersion.jar"}},
		 "pluginDependencies":{"PAPER":[
		   {"name":"ViaBackwards","required":true},
		   {"name":"ViaRewind","required":false}]},
		 "platformDependencies":{"PAPER":["1.8","1.21.8","26.3"]},
		 "platformDependenciesFormatted":{"PAPER":["1.8-26.3"]}},
		{"name":"5.12.1","channel":{"name":"Release"},
		 "downloads":{"PAPER":{"fileInfo":{"name":"ViaVersion-5.12.1.jar","sizeBytes":100,
		   "sha256Hash":"aaaa"},"externalUrl":"https://example.com/via",
		   "downloadUrl":""}},
		 "platformDependencies":{"PAPER":["1.21.8"]},
		 "platformDependenciesFormatted":{"PAPER":["1.21.8"]}}
	]
}`

func TestVersionsParsesAndFilters(t *testing.T) {
	var gotPlatform, gotVersion, gotChannel string
	var channelPresent, versionPresent bool
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/projects/ViaVersion/ViaVersion/versions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		gotPlatform = q.Get("platform")
		gotVersion = q.Get("platformVersion")
		gotChannel = q.Get("channel")
		_, channelPresent = q["channel"]
		_, versionPresent = q["platformVersion"]
		fmt.Fprint(w, versionBody)
	})
	c := &Client{BaseURL: srv.URL}
	vers, err := c.Versions(ctx(), "ViaVersion", "ViaVersion", "PAPER", "1.21.8", "Release")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if gotPlatform != "PAPER" || gotVersion != "1.21.8" || gotChannel != "Release" ||
		!channelPresent || !versionPresent {
		t.Fatalf("query params = %q/%q/%q (%v/%v)", gotPlatform, gotVersion, gotChannel, channelPresent, versionPresent)
	}
	if len(vers) != 2 {
		t.Fatalf("versions = %+v", vers)
	}
	v := vers[0]
	if v.Name != "5.12.2" || v.Channel != "Release" || v.FileName != "ViaVersion-5.12.2.jar" ||
		v.SHA256 != "5D0EAA" || v.SizeBytes != 6519553 ||
		v.DownloadURL != "https://hangarcdn.papermc.io/plugins/ViaVersion.jar" {
		t.Fatalf("version = %+v", v)
	}
	if v.External() {
		t.Fatal("a version with a download URL is not external")
	}
	if len(v.GameVersions) != 3 || v.GameRange != "1.8-26.3" {
		t.Fatalf("game versions = %v, range = %q", v.GameVersions, v.GameRange)
	}
	if len(v.Dependencies) != 1 || v.Dependencies[0] != "ViaBackwards" {
		t.Fatalf("dependencies = %v, only required ones are reported", v.Dependencies)
	}
}

func TestVersionsOmitsEmptyFilters(t *testing.T) {
	var channelPresent, versionPresent bool
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		_, channelPresent = q["channel"]
		_, versionPresent = q["platformVersion"]
		fmt.Fprint(w, `{"result":[]}`)
	})
	c := &Client{BaseURL: srv.URL}
	if _, err := c.Versions(ctx(), "o", "s", "PAPER", "", ""); err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if channelPresent || versionPresent {
		t.Fatalf("empty filters must be omitted (channel=%v version=%v)", channelPresent, versionPresent)
	}
}

func TestVersionsExternalDetection(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, versionBody)
	})
	c := &Client{BaseURL: srv.URL}
	vers, err := c.Versions(ctx(), "ViaVersion", "ViaVersion", "PAPER", "", "")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if !vers[1].External() || vers[1].ExternalURL != "https://example.com/via" {
		t.Fatalf("external version = %+v", vers[1])
	}
}

func TestVersionsRequiresCoordinates(t *testing.T) {
	c := &Client{BaseURL: "http://x"}
	if _, err := c.Versions(ctx(), "", "s", "PAPER", "", ""); err == nil {
		t.Fatal("Versions without owner should fail")
	}
	if _, err := c.Versions(ctx(), "o", "", "PAPER", "", ""); err == nil {
		t.Fatal("Versions without slug should fail")
	}
}

func TestVersionsHTTPError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	})
	c := &Client{BaseURL: srv.URL}
	_, err := c.Versions(ctx(), "o", "s", "PAPER", "", "")
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v", err)
	}
}

func TestVersionsMalformedJSON(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	})
	c := &Client{BaseURL: srv.URL}
	if _, err := c.Versions(ctx(), "o", "s", "PAPER", "", ""); err == nil {
		t.Fatal("Versions should fail on malformed JSON")
	}
}

func TestByHashFound(t *testing.T) {
	const hash = "abc123"
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/versions/hash/"+hash {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"name":"ViaVersion","namespace":{"owner":"ViaVersion","slug":"ViaVersion"},
			"description":"Protocol support","stats":{"downloads":531399}}`)
	})
	c := &Client{BaseURL: srv.URL}
	p, found, err := c.ByHash(ctx(), hash)
	if err != nil {
		t.Fatalf("ByHash: %v", err)
	}
	if !found || p.Name != "ViaVersion" || p.Owner != "ViaVersion" || p.Downloads != 531399 {
		t.Fatalf("project = %+v, found = %v", p, found)
	}
}

func TestByHashUnknownReturnsNotFound(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"Unknown hash"}`)
	})
	c := &Client{BaseURL: srv.URL}
	p, found, err := c.ByHash(ctx(), "deadbeef")
	if err != nil {
		t.Fatalf("a 404 is a normal answer, got %v", err)
	}
	if found || p.Name != "" {
		t.Fatalf("project = %+v, found = %v", p, found)
	}
}

func TestByHashHTTPError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	c := &Client{BaseURL: srv.URL}
	if _, _, err := c.ByHash(ctx(), "abc"); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("error = %v", err)
	}
}

func TestByHashMalformedJSON(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{`)
	})
	c := &Client{BaseURL: srv.URL}
	if _, _, err := c.ByHash(ctx(), "abc"); err == nil {
		t.Fatal("ByHash should fail on malformed JSON")
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	payload := []byte("fake jar bytes")
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/plugin.jar" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write(payload)
	})
	dest := filepath.Join(t.TempDir(), "plugins", "plugin.jar")
	c := &Client{BaseURL: srv.URL}
	v := Version{
		Name:        "1.0.0",
		DownloadURL: srv.URL + "/plugin.jar",
		FileName:    "plugin.jar",
		SHA256:      strings.ToUpper(sha256Hex(payload)),
	}
	if err := c.Download(ctx(), v, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading download: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("content = %q", got)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("the .part file should be gone, stat err = %v", err)
	}
}

func TestDownloadChecksumMismatchRemovesPartial(t *testing.T) {
	payload := []byte("corrupt bytes")
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	})
	dest := filepath.Join(t.TempDir(), "plugin.jar")
	c := &Client{BaseURL: srv.URL}
	v := Version{
		Name:        "1.0.0",
		DownloadURL: srv.URL + "/plugin.jar",
		FileName:    "plugin.jar",
		SHA256:      sha256Hex([]byte("something else")),
	}
	err := c.Download(ctx(), v, dest)
	if err == nil {
		t.Fatal("Download should reject a checksum mismatch")
	}
	msg := err.Error()
	if !strings.Contains(msg, v.SHA256) || !strings.Contains(msg, sha256Hex(payload)) {
		t.Fatalf("error should name both hashes, got %q", msg)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("the final file must not exist after a mismatch, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Fatalf("the partial file must be removed, stat err = %v", statErr)
	}
}

func TestDownloadRejectsExternal(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "plugin.jar")
	c := &Client{}
	for _, v := range []Version{
		{Name: "1", ExternalURL: "https://example.com/p"},
		{Name: "2", DownloadURL: ""},
	} {
		if err := c.Download(ctx(), v, dest); err == nil || !strings.Contains(err.Error(), "externally") {
			t.Fatalf("Download(%+v) error = %v", v, err)
		}
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("an external version must not write anything")
	}
}

func TestDownloadUsesDefaultClient(t *testing.T) {
	payload := []byte("jar")
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	c := &Client{BaseURL: srv.URL}
	dest := filepath.Join(t.TempDir(), "plugin.jar")
	v := Version{Name: "1", DownloadURL: srv.URL + "/p.jar", FileName: "p.jar", SHA256: sha256Hex(payload)}
	if err := c.Download(ctx(), v, dest); err != nil {
		t.Fatalf("Download with the default client: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("stat: %v", err)
	}
}

func TestDownloadCopyFailureRemovesPartial(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// Promise more bytes than are written so io.Copy fails.
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("short"))
	})
	dest := filepath.Join(t.TempDir(), "plugin.jar")
	c := &Client{BaseURL: srv.URL}
	v := Version{Name: "1", DownloadURL: srv.URL + "/p.jar", FileName: "p.jar", SHA256: "aa"}
	if err := c.Download(ctx(), v, dest); err == nil {
		t.Fatal("Download should fail when the body ends early")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatal("a truncated transfer must not leave a partial file")
	}
}

func TestDownloadCreateDirectoryFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("jar")) })
	c := &Client{BaseURL: srv.URL}
	v := Version{Name: "1", DownloadURL: srv.URL + "/p.jar", FileName: "p.jar", SHA256: "aa"}
	err := c.Download(ctx(), v, filepath.Join(blocker, "sub", "p.jar"))
	if err == nil || !strings.Contains(err.Error(), "creating directory") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadCreatePartialFailure(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "p.jar")
	if err := os.Mkdir(dest+".part", 0o755); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("jar")) })
	c := &Client{BaseURL: srv.URL}
	v := Version{Name: "1", DownloadURL: srv.URL + "/p.jar", FileName: "p.jar", SHA256: "aa"}
	err := c.Download(ctx(), v, dest)
	if err == nil || !strings.Contains(err.Error(), "creating") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadRenameFailureRemovesPartial(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "p.jar")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("jar")
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	c := &Client{BaseURL: srv.URL}
	v := Version{Name: "1", DownloadURL: srv.URL + "/p.jar", FileName: "p.jar", SHA256: sha256Hex(payload)}
	err := c.Download(ctx(), v, dest)
	if err == nil || !strings.Contains(err.Error(), "saving") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Fatal("the partial file must be removed when the rename fails")
	}
}

func TestVersionsWithoutPlatformFallsBackToPaper(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, versionBody)
	})
	c := &Client{BaseURL: srv.URL}
	vers, err := c.Versions(ctx(), "o", "s", "", "", "")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vers) != 2 || vers[0].FileName != "ViaVersion-5.12.2.jar" {
		t.Fatalf("versions = %+v", vers)
	}
}

func TestVersionsMissingPlatformEntryIsExternal(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":[{"name":"1.0","channel":{"name":"Release"},
			"platformDependencies":{"PAPER":["1.21"]}}]}`)
	})
	c := &Client{BaseURL: srv.URL}
	vers, err := c.Versions(ctx(), "o", "s", "PAPER", "", "")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vers) != 1 || !vers[0].External() {
		t.Fatalf("versions = %+v, a version without a file must be external", vers)
	}
}

func TestRequestCreationError(t *testing.T) {
	c := &Client{BaseURL: "://bad"}
	if _, err := c.Search(ctx(), "x", "PAPER"); err == nil {
		t.Fatal("Search should fail on an invalid base URL")
	}
	if _, err := c.Versions(ctx(), "o", "s", "PAPER", "", ""); err == nil {
		t.Fatal("Versions should fail on an invalid base URL")
	}
	if _, _, err := c.ByHash(ctx(), "abc"); err == nil {
		t.Fatal("ByHash should fail on an invalid base URL")
	}
}

func TestErrorResponseFallsBackToMessage(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"message":"only a message"}`)
	})
	c := &Client{BaseURL: srv.URL}
	_, err := c.Search(ctx(), "x", "PAPER")
	if err == nil || !strings.Contains(err.Error(), "only a message") {
		t.Fatalf("error = %v", err)
	}
}

func TestErrorResponseIgnoresNonJSON(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<html>bad gateway</html>`)
	})
	c := &Client{BaseURL: srv.URL}
	_, err := c.Search(ctx(), "x", "PAPER")
	if err == nil || strings.Contains(err.Error(), "html") {
		t.Fatalf("error = %v", err)
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	dest := filepath.Join(t.TempDir(), "plugin.jar")
	c := &Client{BaseURL: srv.URL}
	v := Version{Name: "1", DownloadURL: srv.URL + "/missing.jar", FileName: "missing.jar", SHA256: "aa"}
	err := c.Download(ctx(), v, dest)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Fatal("a failed request must not leave a partial file")
	}
}
