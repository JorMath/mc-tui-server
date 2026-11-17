// Package doctor runs the pre-flight checks that explain why a Minecraft
// instance will not start.
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/JorMath/mc-tui-server/internal/backup"
	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/javacheck"
	"github.com/JorMath/mc-tui-server/internal/ports"
	"github.com/JorMath/mc-tui-server/internal/properties"
)

const (
	// propertiesFile is the file Minecraft reads its settings from.
	propertiesFile = "server.properties"
	// eulaFile holds the accepted EULA flag.
	eulaFile = "eula.txt"
	// defaultWorld and defaultPort are the vanilla values used when the
	// properties file does not declare them.
	defaultWorld = "world"
	defaultPort  = 25565
	// backupMaxAgeDays is how old the newest backup may be and still pass.
	backupMaxAgeDays = 7
)

// Check is one diagnostic line. Detail is shown next to the status and is
// written as a short sentence, not a Go error string.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Options override the side-effecting lookups so tests stay hermetic. The
// zero value uses the real implementations.
type Options struct {
	JavaVersion func(ctx context.Context, javaPath string) (int, error)
	Now         func() time.Time
}

// Run checks an instance. others is the full instance registry, used to
// detect a port declared by another instance.
func Run(ctx context.Context, inst config.Instance, others []config.Instance, opt Options) []Check {
	javaVersion := opt.JavaVersion
	if javaVersion == nil {
		javaVersion = javacheck.Version
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	return []Check{
		checkJava(ctx, inst, javaVersion),
		checkEULA(inst),
		checkServerFiles(inst),
		checkWorld(inst),
		checkPort(inst, others),
		checkBackup(inst, now),
	}
}

// checkJava compares the runtime Java with the version Minecraft needs.
func checkJava(ctx context.Context, inst config.Instance, version func(context.Context, string) (int, error)) Check {
	required := javacheck.Required(inst.Version)
	if required == 0 {
		return Check{
			Name:   "Java",
			OK:     true,
			Detail: "Minecraft version not recognized; the Java requirement was not checked",
		}
	}
	got, err := version(ctx, inst.JavaPath)
	if err != nil {
		return Check{Name: "Java", Detail: fmt.Sprintf("The Java version could not be detected: %v", err)}
	}
	if got < required {
		runtimeName := "system Java from PATH"
		if inst.JavaPath != "" {
			runtimeName = inst.JavaPath
		}
		return Check{
			Name: "Java",
			Detail: fmt.Sprintf("Java %d is too old; Minecraft %s needs Java %d (%s)",
				got, inst.Version, required, runtimeName),
		}
	}
	return Check{
		Name:   "Java",
		OK:     true,
		Detail: fmt.Sprintf("Java %d meets the Java %d requirement", got, required),
	}
}

// checkEULA reports whether eula.txt accepts the Minecraft EULA.
func checkEULA(inst config.Instance) Check {
	name := "EULA"
	props, err := properties.Load(filepath.Join(inst.Dir, eulaFile))
	if err != nil {
		return Check{Name: name, Detail: fmt.Sprintf("%s could not be read: %v", eulaFile, err)}
	}
	value, _ := props.Get("eula")
	if strings.EqualFold(strings.TrimSpace(value), "true") {
		return Check{Name: name, OK: true, Detail: fmt.Sprintf("%s accepts the Minecraft EULA", eulaFile)}
	}
	return Check{Name: name, Detail: fmt.Sprintf("%s does not accept the Minecraft EULA", eulaFile)}
}

// checkServerFiles reports whether the file the server is launched from is in
// place: the Forge/NeoForge args file or the server jar.
func checkServerFiles(inst config.Instance) Check {
	name := "Server files"
	if inst.ArgsDir != "" {
		rel := filepath.Join(inst.ArgsDir, argsFileFor(runtime.GOOS))
		if fileExists(filepath.Join(inst.Dir, rel)) {
			return Check{Name: name, OK: true, Detail: fmt.Sprintf("%s is in place", rel)}
		}
		return Check{Name: name, Detail: fmt.Sprintf("%s is missing", rel)}
	}
	if inst.JarPath == "" {
		return Check{Name: name, Detail: "No server jar is configured for this instance"}
	}
	if fileExists(filepath.Join(inst.Dir, inst.JarPath)) {
		return Check{Name: name, OK: true, Detail: fmt.Sprintf("%s is in place", inst.JarPath)}
	}
	return Check{Name: name, Detail: fmt.Sprintf("%s is missing", inst.JarPath)}
}

// argsFileFor mirrors the args file the Forge/NeoForge installers generate for
// the given OS.
func argsFileFor(goos string) string {
	if goos == "windows" {
		return "win_args.txt"
	}
	return "unix_args.txt"
}

// fileExists reports whether path is a readable regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// checkWorld reports whether the level folder has a level.dat.
func checkWorld(inst config.Instance) Check {
	name := "World"
	world := defaultWorld
	props, err := properties.Load(filepath.Join(inst.Dir, propertiesFile))
	if err == nil {
		if value, ok := props.Get("level-name"); ok && strings.TrimSpace(value) != "" {
			world = strings.TrimSpace(value)
		}
	}
	dir := filepath.Join(inst.Dir, world)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Check{Name: name, Detail: fmt.Sprintf("The world folder %s is missing", world)}
	}
	if !fileExists(filepath.Join(dir, "level.dat")) {
		return Check{Name: name, Detail: fmt.Sprintf("The world %s has no level.dat", world)}
	}
	return Check{Name: name, OK: true, Detail: fmt.Sprintf("The world %s is ready", world)}
}

// checkPort reports whether the declared port passes validation against the
// rest of the registry.
func checkPort(inst config.Instance, others []config.Instance) Check {
	name := "Port"
	port := defaultPort
	props, err := properties.Load(filepath.Join(inst.Dir, propertiesFile))
	if err == nil {
		if raw, ok := props.Get("server-port"); ok {
			if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
				port = parsed
			}
		}
	}
	if err := ports.Validate(others, inst.Name, port); err != nil {
		return Check{Name: name, Detail: capitalize(err.Error()) + "."}
	}
	return Check{Name: name, OK: true, Detail: fmt.Sprintf("Port %d is available", port)}
}

// checkBackup reports whether the newest backup is younger than a week.
func checkBackup(inst config.Instance, now func() time.Time) Check {
	name := "Backup"
	names, err := backup.List(inst.Dir)
	if err != nil {
		return Check{Name: name, Detail: fmt.Sprintf("The backups folder could not be read: %v", err)}
	}
	if len(names) == 0 {
		return Check{Name: name, Detail: "No backups yet"}
	}
	var (
		newest     string
		newestTime time.Time
	)
	for _, backupName := range names {
		path := filepath.Join(inst.Dir, backup.Dir, backupName)
		mod := modTime(path)
		if newest == "" || mod.After(newestTime) {
			newest, newestTime = path, mod
		}
	}
	days := int(now().Sub(newestTime).Hours() / 24)
	if days >= backupMaxAgeDays {
		return Check{Name: name, Detail: fmt.Sprintf("The newest backup is %d days old", days)}
	}
	return Check{Name: name, OK: true, Detail: fmt.Sprintf("The newest backup is %d days old", days)}
}

// modTime returns the modification time of path, or the zero time when it
// cannot be read.
func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// capitalize upper-cases the first letter of s so an error string reads as a
// sentence.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
