// Package installer descarga y ejecuta los installers oficiales de
// Forge, NeoForge y Quilt para montar el runtime de servidor que exige un
// modpack, y detecta cómo arrancar el resultado (args-file o jar único).
package installer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// run ejecuta el installer y reenvía cada línea de salida a logf.
func run(ctx context.Context, dir string, logf func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("opening installer output: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("running %s (is Java on your PATH?): %w", name, err)
	}
	scanLines(out, logf)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("the installer failed: %w", err)
	}
	return nil
}

func scanLines(r io.Reader, logf func(string)) {
	if logf == nil {
		logf = func(string) {}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			logf(line)
		}
	}
}

// RunForgeLike ejecuta un installer de Forge/NeoForge en el directorio de
// la instancia (descarga librerías y el server vanilla; tarda minutos).
func RunForgeLike(ctx context.Context, java, installerJar, dir string, logf func(string)) error {
	if java == "" {
		java = "java"
	}
	return run(ctx, dir, logf, java, "-jar", installerJar, "--installServer")
}

// RunQuilt ejecuta el installer de Quilt, que deja un
// quilt-server-launch.jar y descarga el server vanilla. loaderVersion
// vacío instala el loader más reciente.
func RunQuilt(ctx context.Context, java, installerJar, dir, mc, loaderVersion string, logf func(string)) error {
	if java == "" {
		java = "java"
	}
	args := []string{"-jar", installerJar, "install", "server", mc}
	if loaderVersion != "" {
		args = append(args, loaderVersion)
	}
	args = append(args, "--install-dir=.", "--download-server")
	return run(ctx, dir, logf, java, args...)
}

// argsDirGlobs son las rutas (relativas a la instancia) donde los
// installers modernos dejan win_args.txt/unix_args.txt.
var argsDirGlobs = []string{
	"libraries/net/minecraftforge/forge/*",
	"libraries/net/neoforged/neoforge/*",
	"libraries/net/neoforged/forge/*",
}

// launchJarGlobs son los jars de arranque que dejan los installers viejos
// (Forge ≤1.16) y el de Quilt.
var launchJarGlobs = []string{"quilt-server-launch.jar", "forge-*.jar", "neoforge-*.jar"}

// DetectLaunch inspecciona una instancia recién instalada y devuelve cómo
// arrancarla: un directorio de args-file (Forge/NeoForge modernos) o un jar.
func DetectLaunch(dir string) (argsDir, jarPath string, err error) {
	for _, g := range argsDirGlobs {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(g)))
		for _, m := range matches {
			for _, f := range []string{"win_args.txt", "unix_args.txt"} {
				if _, statErr := os.Stat(filepath.Join(m, f)); statErr == nil {
					rel, relErr := filepath.Rel(dir, m)
					if relErr != nil {
						return "", "", relErr
					}
					return rel, "", nil
				}
			}
		}
	}
	for _, g := range launchJarGlobs {
		matches, _ := filepath.Glob(filepath.Join(dir, g))
		for _, m := range matches {
			base := filepath.Base(m)
			if strings.HasSuffix(base, "-installer.jar") {
				continue
			}
			return "", base, nil
		}
	}
	return "", "", fmt.Errorf("could not find how to launch the installed server (no args file or launch jar)")
}
