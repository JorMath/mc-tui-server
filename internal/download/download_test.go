package download

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mc-tui-server/internal/config"
)

func TestForKnownTypes(t *testing.T) {
	for _, typ := range []config.ServerType{config.Vanilla, config.Paper, config.Purpur, config.Fabric} {
		p, err := For(typ, nil)
		if err != nil {
			t.Fatalf("For(%s): %v", typ, err)
		}
		if p.Name() != string(typ) {
			t.Fatalf("Name() = %q, quiero %q", p.Name(), typ)
		}
	}
}

func TestForUnknownTypeFails(t *testing.T) {
	if _, err := For(config.ServerType("forge"), nil); err == nil {
		t.Fatal("For con tipo desconocido debe fallar")
	}
}

func TestGetJSONInvalidURLFails(t *testing.T) {
	var v any
	if err := GetJSON(context.Background(), nil, "http://\x00invalido", &v); err == nil {
		t.Fatal("GetJSON con URL inválida debe fallar")
	}
}

func TestGetJSONConnectionErrorFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // servidor ya cerrado: client.Do falla
	var v any
	if err := GetJSON(context.Background(), nil, srv.URL, &v); err == nil {
		t.Fatal("GetJSON contra servidor caído debe fallar")
	}
}

func TestGetJSONBadStatusFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	var v any
	if err := GetJSON(context.Background(), nil, srv.URL, &v); err == nil {
		t.Fatal("GetJSON con HTTP 404 debe fallar")
	}
}

func TestGetJSONBadBodyFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{no es json"))
	}))
	defer srv.Close()
	var v any
	if err := GetJSON(context.Background(), nil, srv.URL, &v); err == nil {
		t.Fatal("GetJSON con cuerpo corrupto debe fallar")
	}
}

func TestDownloadFile(t *testing.T) {
	payload := []byte("contenido del jar de prueba")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "srv", "server.jar")
	var lastDone, lastTotal int64
	// Cliente explícito para cubrir la rama no-nil de orDefault.
	err := DownloadFile(context.Background(), srv.Client(), srv.URL, dest, func(done, total int64) {
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("leyendo destino: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("contenido = %q, quiero %q", got, payload)
	}
	if lastDone != int64(len(payload)) || lastTotal != int64(len(payload)) {
		t.Fatalf("progreso final = %d/%d, quiero %d/%d", lastDone, lastTotal, len(payload), len(payload))
	}
}

func TestDownloadFileNilProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "server.jar")
	if err := DownloadFile(context.Background(), nil, srv.URL, dest, nil); err != nil {
		t.Fatalf("DownloadFile sin progreso: %v", err)
	}
}

func TestDownloadFileInvalidURLFails(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "server.jar")
	if err := DownloadFile(context.Background(), nil, "http://\x00", dest, nil); err == nil {
		t.Fatal("DownloadFile con URL inválida debe fallar")
	}
}

func TestDownloadFileConnectionErrorFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	dest := filepath.Join(t.TempDir(), "server.jar")
	if err := DownloadFile(context.Background(), nil, srv.URL, dest, nil); err == nil {
		t.Fatal("DownloadFile contra servidor caído debe fallar")
	}
}

func TestDownloadFileBadStatusFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "server.jar")
	if err := DownloadFile(context.Background(), nil, srv.URL, dest, nil); err == nil {
		t.Fatal("DownloadFile con HTTP 500 debe fallar")
	}
}

func TestDownloadFileMkdirFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "bloqueo")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(blocker, "sub", "server.jar")
	if err := DownloadFile(context.Background(), nil, srv.URL, dest, nil); err == nil {
		t.Fatal("DownloadFile con padre-archivo debe fallar")
	}
}

func TestDownloadFileCreateFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	// El destino es un directorio existente: os.Create falla.
	if err := DownloadFile(context.Background(), nil, srv.URL, t.TempDir(), nil); err == nil {
		t.Fatal("DownloadFile sobre un directorio debe fallar")
	}
}

func TestDownloadFileTruncatedBodyFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.Write([]byte("corto"))
		w.(http.Flusher).Flush() // asegura que los headers salgan antes del corte
		panic(http.ErrAbortHandler) // corta la conexión a mitad del cuerpo
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "server.jar")
	if err := DownloadFile(context.Background(), nil, srv.URL, dest, nil); err == nil {
		t.Fatal("DownloadFile con cuerpo truncado debe fallar")
	}
}
