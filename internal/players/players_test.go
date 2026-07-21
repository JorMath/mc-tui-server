package players

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	entries, err := Load(filepath.Join(t.TempDir(), WhitelistFile))
	if err != nil || entries != nil {
		t.Fatalf("Load = %v, %v", entries, err)
	}
}

func TestSaveLoadRoundTripPreservesUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), OpsFile)
	entries := []Entry{
		{"uuid": "u1", "name": "Alice", "level": 4, "campoRaro": "x"},
	}
	if err := Save(path, entries); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 || got[0]["campoRaro"] != "x" || got[0]["name"] != "Alice" {
		t.Fatalf("roundtrip = %+v", got)
	}
}

func TestNamesHasRemove(t *testing.T) {
	entries := []Entry{Whitelist("u1", "Alice"), Whitelist("u2", "Bob")}
	if n := Names(entries); len(n) != 2 || n[0] != "Alice" {
		t.Fatalf("Names = %v", n)
	}
	if !Has(entries, "alice") {
		t.Fatal("Has debe ignorar mayúsculas")
	}
	entries, removed := Remove(entries, "ALICE")
	if !removed || len(entries) != 1 || entries[0]["name"] != "Bob" {
		t.Fatalf("Remove = %v, %v", entries, removed)
	}
	if _, removed := Remove(entries, "nadie"); removed {
		t.Fatal("Remove de un nombre ausente no debe reportar éxito")
	}
}

func TestBanEntryShape(t *testing.T) {
	e := Ban("u1", "Griefer", time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC))
	if e["expires"] != "forever" || e["name"] != "Griefer" || e["uuid"] != "u1" {
		t.Fatalf("Ban = %+v", e)
	}
}

func TestMojangUUIDFormatsDashes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/users/profiles/minecraft/Notch", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"069a79f444e94726a5befca90e38aaf5","name":"Notch"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	uuid, err := MojangUUID(context.Background(), nil, srv.URL, "Notch")
	if err != nil {
		t.Fatalf("MojangUUID: %v", err)
	}
	if uuid != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
		t.Fatalf("uuid = %q", uuid)
	}
}

func TestMojangUUIDNotFoundFails(t *testing.T) {
	mux := http.NewServeMux() // 404 para todo
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if _, err := MojangUUID(context.Background(), nil, srv.URL, "NoExiste"); err == nil {
		t.Fatal("jugador inexistente debe fallar")
	}
}

func TestOfflineUUID(t *testing.T) {
	// Valor conocido del algoritmo OfflinePlayer para "Notch".
	got := OfflineUUID("Notch")
	if len(got) != 36 || got[14] != '3' {
		t.Fatalf("OfflineUUID = %q, debe ser un UUID v3", got)
	}
	if got != OfflineUUID("Notch") {
		t.Fatal("OfflineUUID debe ser determinista")
	}
	if got == OfflineUUID("notch") {
		t.Fatal("OfflineUUID distingue mayúsculas (así lo hace el server)")
	}
}
