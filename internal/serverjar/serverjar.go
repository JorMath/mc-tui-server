// Package serverjar replaces the server jar of an instance safely: it
// downloads the new file to a staging path, validates that it is a real jar,
// backs up the previous jar with the old version in its name and only then
// rotates the new file into place.
package serverjar

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/download"
)

// DownloadFunc descarga url en dest. download.DownloadFile encaja sin
// adaptador cuando no hace falta un contexto propio.
type DownloadFunc func(url, dest string, progress func(done, total int64)) error

// jarMagic son los primeros bytes de cualquier archivo ZIP/JAR.
var jarMagic = []byte{'P', 'K', 3, 4}

// Path devuelve la ruta absoluta del jar de la instancia. JarPath puede ser
// relativo al directorio de la instancia (así lo guarda el asistente).
func Path(inst config.Instance) string {
	if inst.JarPath == "" || filepath.IsAbs(inst.JarPath) {
		return inst.JarPath
	}
	return filepath.Join(inst.Dir, inst.JarPath)
}

// Latest devuelve la versión más nueva que ofrece el provider (los
// providers las listan de nueva a vieja) cuando supera a current. Con
// current vacío cualquier versión cuenta como actualización.
func Latest(versions []string, current string) (string, bool) {
	if len(versions) == 0 {
		return "", false
	}
	latest := versions[0]
	if current == "" {
		return latest, true
	}
	return latest, download.CompareVersions(latest, current) > 0
}

// BackupName arma el nombre del jar anterior, con la versión vieja cuando se
// conoce: server.jar.bak-1.21.7.
func BackupName(jarPath, version string) string {
	name := jarPath + ".bak"
	if v := safeVersion(version); v != "" {
		name += "-" + v
	}
	return name
}

// safeVersion deja solo caracteres seguros para un nombre de archivo.
func safeVersion(version string) string {
	var b strings.Builder
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// Stage descarga el jar nuevo junto al actual y comprueba que sea un jar
// real. Devuelve la ruta del archivo temporal listo para Commit; ante
// cualquier fallo lo borra y deja el jar instalado intacto.
func Stage(jarPath, url string, dl DownloadFunc, progress func(done, total int64)) (string, error) {
	staged := jarPath + ".new"
	_ = os.Remove(staged)
	if err := dl(url, staged, progress); err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	if err := validate(staged); err != nil {
		_ = os.Remove(staged)
		return "", err
	}
	return staged, nil
}

// validate comprueba que el archivo empiece con la firma ZIP/JAR.
func validate(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return fmt.Errorf("%s is not a valid jar: %w", filepath.Base(path), err)
	}
	if !bytes.Equal(magic[:], jarMagic) {
		return fmt.Errorf("%s is not a valid jar (missing PK signature)", filepath.Base(path))
	}
	return nil
}

// Commit rota los archivos: el jar actual pasa a BackupName y el staged
// ocupa su lugar. Devuelve la ruta del backup ("" si era la primera
// instalación). Si el segundo rename falla intenta restaurar el anterior.
func Commit(jarPath, staged, oldVersion string) (backup string, err error) {
	if _, statErr := os.Stat(jarPath); statErr == nil {
		backup = BackupName(jarPath, oldVersion)
		_ = os.Remove(backup)
		if err := os.Rename(jarPath, backup); err != nil {
			return "", fmt.Errorf("moving the current jar to %s: %w", filepath.Base(backup), err)
		}
		if err := os.Rename(staged, jarPath); err != nil {
			if restoreErr := os.Rename(backup, jarPath); restoreErr != nil {
				return backup, fmt.Errorf("installing %s: %w (restoring the previous jar also failed: %v)",
					filepath.Base(jarPath), err, restoreErr)
			}
			return "", fmt.Errorf("installing %s: %w", filepath.Base(jarPath), err)
		}
		return backup, nil
	}
	if err := os.Rename(staged, jarPath); err != nil {
		return "", fmt.Errorf("installing %s: %w", filepath.Base(jarPath), err)
	}
	return "", nil
}

// Discard borra el archivo temporal de Stage.
func Discard(staged string) {
	if staged != "" {
		_ = os.Remove(staged)
	}
}
