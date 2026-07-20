// lifecycle.go: arranque/parada/reinicio de la instancia seleccionada (R1)
// y muestreo periódico de estados y métricas CPU/RAM (R5).
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/JorMath/mc-tui-server/internal/config"
	"github.com/JorMath/mc-tui-server/internal/javacheck"
	"github.com/JorMath/mc-tui-server/internal/mcping"
	"github.com/JorMath/mc-tui-server/internal/metrics"
	"github.com/JorMath/mc-tui-server/internal/properties"
	"github.com/JorMath/mc-tui-server/internal/server"
)

// checkJava avisa y bloquea el arranque si el Java disponible es más
// viejo que el que exige la versión de MC de la instancia. Si no se puede
// determinar alguna de las dos versiones, no bloquea.
func (a *app) checkJava(inst config.Instance) bool {
	required := javacheck.Required(inst.Version)
	if required == 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	major, err := javacheck.Version(ctx, inst.JavaPath)
	if err != nil {
		// Sin java el Start fallará con su propio error claro.
		return true
	}
	if major < required {
		a.appendLog(inst.Name, fmt.Sprintf(
			"[mc-tui] Java %d found, but Minecraft %s needs Java %d+ — install it or set java_path for this instance",
			major, inst.Version, required))
		return false
	}
	return true
}

// startSelected/stopSelected/restartSelected corren en goroutines porque
// Stop puede bloquear hasta stopTimeout y no debe congelar la UI.
func (a *app) startSelected() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	go func() {
		if !a.checkJava(mgr.Instance()) {
			return
		}
		if err := mgr.Start(); err != nil {
			a.appendLog(mgr.Instance().Name, "[mc-tui] "+err.Error())
		}
	}()
}

func (a *app) stopSelected() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	go func() {
		if err := mgr.Stop(stopTimeout); err != nil {
			a.appendLog(mgr.Instance().Name, "[mc-tui] "+err.Error())
		}
	}()
}

func (a *app) restartSelected() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	go func() {
		if err := mgr.Restart(stopTimeout); err != nil {
			a.appendLog(mgr.Instance().Name, "[mc-tui] "+err.Error())
		}
	}()
}

func (a *app) refreshStatuses() {
	// Auto-descarta el splash tras ~3s (6 ticks de 500ms).
	if a.splash.Get() {
		a.splashTicks++
		if a.splashTicks >= 6 {
			a.splash.Set(false)
		}
	}
	a.statuses.Update(func(m map[string]server.Status) map[string]server.Status {
		for _, mgr := range a.managers.Get() {
			name := mgr.Instance().Name
			st := mgr.Status()
			if st == server.Crashed && m[name] != server.Crashed && m[name] != "" {
				a.onCrash(mgr)
			}
			m[name] = st
		}
		return m
	})
	a.refreshSamples()
	a.refreshPings()
}

// worldName lee level-name de server.properties ("world" por defecto).
func worldName(inst config.Instance) string {
	if props, err := properties.Load(filepath.Join(inst.Dir, "server.properties")); err == nil {
		if v, ok := props.Get("level-name"); ok && v != "" {
			return v
		}
	}
	return "world"
}

// serverPort lee server-port de server.properties (25565 por defecto).
func serverPort(inst config.Instance) string {
	if props, err := properties.Load(filepath.Join(inst.Dir, "server.properties")); err == nil {
		if v, ok := props.Get("server-port"); ok && v != "" {
			return v
		}
	}
	return "25565"
}

// refreshPings consulta el server-list-ping de las instancias corriendo
// cada ~5s (10 ticks de 500ms) para mostrar jugadores online.
func (a *app) refreshPings() {
	a.pingTick++
	if a.pingTick%10 != 0 {
		return
	}
	for _, mgr := range a.managers.Get() {
		inst := mgr.Instance()
		name := inst.Name
		if mgr.Status() != server.Running {
			a.pings.Update(func(m map[string]mcping.Status) map[string]mcping.Status {
				delete(m, name)
				return m
			})
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			st, err := mcping.Ping(ctx, "127.0.0.1:"+serverPort(inst))
			a.pings.Update(func(m map[string]mcping.Status) map[string]mcping.Status {
				if err != nil {
					// Arrancando todavía o sin responder: sin datos frescos.
					delete(m, name)
					return m
				}
				m[name] = st
				return m
			})
		}()
	}
}

// playersText es la línea de jugadores del sidebar ("" si no hay ping).
func (a *app) playersText(name string) string {
	st, ok := a.pings.Get()[name]
	if !ok {
		return ""
	}
	return fmt.Sprintf("players %d/%d", st.Online, st.Max)
}

// onCrash registra el crash y, si la instancia tiene auto-restart, la
// reinicia tras 5s — con tope de 3 crashes en 10 minutos para no ciclar
// sobre un servidor roto.
func (a *app) onCrash(mgr *server.Manager) {
	name := mgr.Instance().Name
	a.appendLog(name, "[mc-tui] Server crashed (exited with an error)")
	inst, ok := a.store.Get(name)
	if !ok || !inst.AutoRestart {
		return
	}
	now := time.Now()
	recent := a.crashTimes[name][:0]
	for _, t := range a.crashTimes[name] {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 3 {
		a.crashTimes[name] = recent
		a.appendLog(name, "[mc-tui] Auto-restart disabled for now: 3 crashes in 10 minutes")
		return
	}
	a.crashTimes[name] = append(recent, now)
	a.appendLog(name, "[mc-tui] Auto-restarting in 5s...")
	time.AfterFunc(5*time.Second, func() {
		if mgr.Status() != server.Crashed {
			return
		}
		if err := mgr.Start(); err != nil {
			a.appendLog(name, "[mc-tui] Auto-restart failed: "+err.Error())
			return
		}
		a.appendLog(name, "[mc-tui] Auto-restarted after crash")
	})
}

// toggleAutoRestart activa/desactiva el reinicio automático de la
// instancia seleccionada (persistido en el JSON).
func (a *app) toggleAutoRestart() {
	mgr := a.current()
	if mgr == nil {
		return
	}
	name := mgr.Instance().Name
	inst, ok := a.store.Get(name)
	if !ok {
		return
	}
	inst.AutoRestart = !inst.AutoRestart
	if err := a.store.Update(inst); err != nil {
		a.appendLog(name, "[mc-tui] Error: "+err.Error())
		return
	}
	if err := a.store.Save(); err != nil {
		a.appendLog(name, "[mc-tui] Error: "+err.Error())
		return
	}
	// Best effort: el manager detenido también recibe la copia nueva.
	_ = mgr.SetInstance(inst)
	if inst.AutoRestart {
		a.appendLog(name, "[mc-tui] Auto-restart ON")
	} else {
		a.appendLog(name, "[mc-tui] Auto-restart OFF")
	}
}

// autoRestartOn indica si la instancia tiene auto-restart (para el sidebar).
func (a *app) autoRestartOn(name string) bool {
	inst, ok := a.store.Get(name)
	return ok && inst.AutoRestart
}

// refreshSamples muestrea CPU/RAM (R5) de las instancias corriendo.
func (a *app) refreshSamples() {
	a.samples.Update(func(m map[string]metrics.Sample) map[string]metrics.Sample {
		for _, mgr := range a.managers.Get() {
			name := mgr.Instance().Name
			pid := mgr.PID()
			if old := a.lastPIDs[name]; old != 0 && old != pid {
				a.collector.Forget(old)
			}
			a.lastPIDs[name] = pid
			if pid == 0 {
				delete(m, name)
				continue
			}
			s, err := a.collector.Sample(pid)
			if err != nil {
				// El proceso pudo morir entre PID() y Sample(); se limpia solo.
				delete(m, name)
				continue
			}
			m[name] = s
		}
		return m
	})
}

// autoMark es el sufijo del nombre en el sidebar cuando el auto-restart
// está activo.
func autoMark(on bool) string {
	if on {
		return " ↻"
	}
	return ""
}

func (a *app) statusText(name string) string {
	st := a.statuses.Get()[name]
	if st == "" {
		st = server.Stopped
	}
	return string(st)
}

func (a *app) metricText(name string) string {
	s, ok := a.samples.Get()[name]
	if !ok {
		return ""
	}
	return fmt.Sprintf("cpu %.1f%% · ram %dM", s.CPUPercent, s.RSSBytes/(1024*1024))
}
