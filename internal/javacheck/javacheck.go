// Package javacheck detecta la versión mayor del Java disponible y la
// mínima que exige cada versión de Minecraft, para avisar ANTES del
// críptico UnsupportedClassVersionError.
package javacheck

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// versionRe caza `version "17.0.9"`, `version "1.8.0_392"` o `version "21"`.
var versionRe = regexp.MustCompile(`version "(\d+)(?:\.(\d+))?`)

// parseMajor extrae la versión mayor de la salida de `java -version`.
// El esquema viejo "1.8" significa Java 8.
func parseMajor(out string) (int, error) {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("could not parse java version from %q", firstLine(out))
	}
	major, _ := strconv.Atoi(m[1])
	if major == 1 && m[2] != "" {
		major, _ = strconv.Atoi(m[2])
	}
	return major, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

var (
	cacheMu sync.Mutex
	cache   = map[string]int{}
)

// Version devuelve la versión mayor del java dado ("" usa el del PATH).
// El resultado se cachea por ruta durante la sesión.
func Version(ctx context.Context, javaPath string) (int, error) {
	if javaPath == "" {
		javaPath = "java"
	}
	cacheMu.Lock()
	if v, ok := cache[javaPath]; ok {
		cacheMu.Unlock()
		return v, nil
	}
	cacheMu.Unlock()

	// `java -version` escribe a stderr; CombinedOutput captura ambos.
	out, err := exec.CommandContext(ctx, javaPath, "-version").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("running %s -version: %w", javaPath, err)
	}
	major, err := parseMajor(string(out))
	if err != nil {
		return 0, err
	}
	cacheMu.Lock()
	cache[javaPath] = major
	cacheMu.Unlock()
	return major, nil
}

// Required devuelve la versión mínima de Java para una versión de
// Minecraft, o 0 si no se reconoce la versión (no se chequea).
// 1.20.5+ y el esquema nuevo (26.x) exigen 21; 1.17–1.20.4 exigen 17
// (1.17 pedía 16, pero 17 lo cubre); lo anterior corre con 8.
func Required(mc string) int {
	parts := strings.Split(mc, ".")
	if len(parts) < 2 {
		return 0
	}
	first, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	if first != 1 {
		// Esquema de versionado nuevo sin prefijo "1." (26.2, ...).
		return 21
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	patch := 0
	if len(parts) > 2 {
		patch, _ = strconv.Atoi(parts[2])
	}
	switch {
	case minor >= 21, minor == 20 && patch >= 5:
		return 21
	case minor >= 17:
		return 17
	default:
		return 8
	}
}
