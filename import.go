// import.go: importar una carpeta de servidor ya montada como instancia
// (v0.3.1) — detecta cómo arrancarla y de qué tipo es sin mover archivos.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/installer"
	"github.com/JorMath/mc-tui-server/internal/server"
)

// detectImport valida la carpeta y deduce el modo de arranque (args-file
// o jar) y el tipo de servidor con heurísticas sobre su contenido.
func detectImport(dir string) (jarPath, argsDir string, typ config.ServerType, err error) {
	info, statErr := os.Stat(dir)
	if statErr != nil || !info.IsDir() {
		return "", "", "", fmt.Errorf("folder not found: %s", dir)
	}
	if argsDirDet, jarDet, detErr := installer.DetectLaunch(dir); detErr == nil {
		switch {
		case argsDirDet != "":
			typ = config.Forge
			if strings.Contains(filepath.ToSlash(argsDirDet), "neoforged") {
				typ = config.NeoForge
			}
			return "", argsDirDet, typ, nil
		case jarDet == "quilt-server-launch.jar":
			return jarDet, "", config.Quilt, nil
		default: // forge-*.jar / neoforge-*.jar viejos
			typ = config.Forge
			if strings.HasPrefix(jarDet, "neoforge") {
				typ = config.NeoForge
			}
			return jarDet, "", typ, nil
		}
	}
	// Sin runtime de loader instalado: buscar un jar de servidor plano.
	jar := ""
	if _, err := os.Stat(filepath.Join(dir, "server.jar")); err == nil {
		jar = "server.jar"
	} else {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.jar"))
		for _, m := range matches {
			base := filepath.Base(m)
			if strings.HasSuffix(base, "-installer.jar") {
				continue
			}
			jar = base
			break
		}
	}
	if jar == "" {
		return "", "", "", fmt.Errorf("no server jar found in %s", dir)
	}
	switch {
	case dirExists(filepath.Join(dir, "mods")):
		typ = config.Fabric // mejor conjetura para un server plano con mods
	case dirExists(filepath.Join(dir, "plugins")):
		typ = config.Paper
	default:
		typ = config.Vanilla
	}
	return jar, "", typ, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// wizSubmitImportPath valida la ruta escrita y pasa al nombre.
func (a *app) wizSubmitImportPath() {
	dir := strings.TrimSpace(a.wizImpPath.Get())
	if dir == "" {
		a.wizMsg.Set("Type the full path of the server folder")
		return
	}
	dir = filepath.Clean(dir)
	jar, argsDir, typ, err := detectImport(dir)
	if err != nil {
		a.wizMsg.Set("Error: " + err.Error())
		return
	}
	a.impDir, a.impJar, a.impArgsDir, a.impType = dir, jar, argsDir, typ
	// Prefill del nombre con la carpeta, filtrando caracteres inválidos.
	var name []rune
	for _, r := range filepath.Base(dir) {
		if validNameChar(r) {
			name = append(name, r)
		}
	}
	a.wizName.Set(string(name))
	a.wizMsg.Set(fmt.Sprintf("Detected: %s (%s)", typ, launchDesc(jar, argsDir)))
	a.wizStep.Set(wizName)
}

func launchDesc(jar, argsDir string) string {
	if argsDir != "" {
		return "args-file launch"
	}
	return jar
}

// wizFinishImport registra la carpeta como instancia sin mover archivos.
func (a *app) wizFinishImport() {
	name := a.wizName.Get()
	inst := config.Instance{
		Name:     name,
		Dir:      a.impDir,
		JarPath:  a.impJar,
		ArgsDir:  a.impArgsDir,
		MemoryMB: a.wizMemoryMB(),
		Type:     a.impType,
		Version:  strings.TrimSpace(a.wizImpVer.Get()),
	}
	// El usuario aceptó el EULA en el paso anterior; se escribe solo si falta.
	eula := filepath.Join(inst.Dir, "eula.txt")
	if _, err := os.Stat(eula); err != nil {
		_ = os.WriteFile(eula, []byte("eula=true\n"), 0o644)
	}
	if err := a.store.Add(inst); err != nil {
		a.wizFail(a.wizGen.Get(), err)
		return
	}
	if err := a.store.Save(); err != nil {
		a.wizFail(a.wizGen.Get(), err)
		return
	}
	mgr := server.New(inst)
	a.pumpLogs(mgr)
	a.managers.Update(func(ms []*server.Manager) []*server.Manager {
		return append(ms, mgr)
	})
	a.appendLog(name, fmt.Sprintf("[mc-tui] Imported %s as a %s server (%s)",
		inst.Dir, inst.Type, launchDesc(inst.JarPath, inst.ArgsDir)))
	if inst.Version == "" {
		a.appendLog(name, "[mc-tui] No Minecraft version set: Modrinth searches will not filter by version")
	}
	a.selected.Set(len(a.managers.Get()) - 1)
	a.wizClose()
}
