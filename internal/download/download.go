// Package download resuelve y descarga los .jar de servidor desde las API
// oficiales de cada distribución (R4): Mojang (vanilla), PaperMC, PurpurMC
// y FabricMC.
package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/JorMath/mc-tui-server/internal/config"
)

// Provider expone las versiones disponibles de una distribución y resuelve
// la URL directa de descarga del jar de servidor.
type Provider interface {
	Name() string
	Versions(ctx context.Context) ([]string, error)
	ResolveJarURL(ctx context.Context, version string) (string, error)
}

// For devuelve el Provider para el tipo de servidor dado. client puede ser
// nil (se usa http.DefaultClient).
func For(t config.ServerType, client *http.Client) (Provider, error) {
	switch t {
	case config.Vanilla:
		return &Vanilla{Client: client}, nil
	case config.Paper:
		return &Paper{Client: client}, nil
	case config.Purpur:
		return &Purpur{Client: client}, nil
	case config.Fabric:
		return &Fabric{Client: client}, nil
	default:
		return nil, fmt.Errorf("unsupported server type: %q", t)
	}
}

func orDefault(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}

// GetJSON hace GET a url y decodifica la respuesta JSON en v.
func GetJSON(ctx context.Context, client *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request to %s: %w", url, err)
	}
	resp, err := orDefault(client).Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("parsing response from %s: %w", url, err)
	}
	return nil
}

// progressWriter invoca el callback con los bytes acumulados.
type progressWriter struct {
	done     int64
	total    int64
	progress func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	p.progress(p.done, p.total)
	return len(b), nil
}

// DownloadFile descarga url en dest, creando los directorios padre.
// progress es opcional y recibe (bytes descargados, total); total es -1 si
// el servidor no lo informa.
func DownloadFile(ctx context.Context, client *http.Client, url, dest string, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request to %s: %w", url, err)
	}
	resp, err := orDefault(client).Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("creating directory for %s: %w", dest, err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	defer f.Close()

	var w io.Writer = f
	if progress != nil {
		w = io.MultiWriter(f, &progressWriter{total: resp.ContentLength, progress: progress})
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}
	return nil
}
