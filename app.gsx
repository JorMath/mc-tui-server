package main

import (
	"fmt"
	"math"

	"github.com/JorMath/mc-tui-server/internal/server"
	tui "github.com/grindlemire/go-tui"
)

// HintsRow pinta una fila de atajos: tecla en color + etiqueta dim.
// Las clases deben ser literales (go-tui ignora class dinámica), de ahí
// el flag green en lugar de un color parametrizado.
templ HintsRow(hints []hint, green bool) {
	<div class="flex gap-1 shrink-0">
		for i, h := range hints {
			if i > 0 {
				<span class="font-dim">|</span>
			}
			if green {
				<span class="text-green font-bold">{h.K}</span>
			} else {
				<span class="text-cyan font-bold">{h.K}</span>
			}
			<span class="font-dim">{h.L}</span>
		}
	</div>
}

// Sidebar lista las instancias con su estado y métricas. El ancho mínimo
// se adapta al tamaño de la terminal.
templ Sidebar(a *app) {
	<div class="flex-col border-rounded p-1 shrink-0" minWidth={a.sidebarMinWidth()}>
		<span class="font-bold shrink-0">Instances</span>
		if len(a.managers.Get()) == 0 {
			<span class="font-dim">No instances yet.</span>
			<span class="font-dim">Press n to create one</span>
		}
		for i, mgr := range a.managers.Get() {
			<div class="flex-col">
				<div class="flex justify-between">
					if i == a.selected.Get() {
						<span class="font-bold text-cyan">{fmt.Sprintf("> %s", mgr.Instance().Name)}</span>
					} else {
						<span>{fmt.Sprintf("  %s", mgr.Instance().Name)}</span>
					}
					if a.statusText(mgr.Instance().Name) == string(server.Running) {
						<span class="text-green">{a.statusText(mgr.Instance().Name)}</span>
					} else if a.statusText(mgr.Instance().Name) == string(server.Stopping) {
						<span class="text-yellow">{a.statusText(mgr.Instance().Name)}</span>
					} else {
						<span class="font-dim">{a.statusText(mgr.Instance().Name)}</span>
					}
				</div>
				if a.metricText(mgr.Instance().Name) != "" {
					<span class="font-dim">{a.metricText(mgr.Instance().Name)}</span>
				}
			</div>
		}
	</div>
}

// ConsoleView muestra el log en vivo de la instancia seleccionada,
// anclado abajo (scrollOffset MaxInt).
templ ConsoleView(a *app) {
	<div
		class="flex-col border-rounded p-1 flex-grow"
		scrollable={tui.ScrollVertical}
		scrollOffset={0, math.MaxInt}
	>
		<span class="font-bold shrink-0">{fmt.Sprintf("Console — %s", a.currentName())}</span>
		for _, line := range a.currentLogs() {
			<span>{line}</span>
		}
	</div>
}

// FooterBar es la línea inferior: barra de comandos, input de rename,
// confirmación de borrado o los atajos globales, según el modo activo.
templ FooterBar(a *app) {
	if a.cmdActive.Get() {
		<div class="flex gap-1 shrink-0 px-1">
			<span class="text-cyan font-bold">{fmt.Sprintf("%s >", a.currentName())}</span>
			<span>{a.cmdText.Get()}</span>
			<span class="text-cyan blink">_</span>
			<span class="text-cyan font-bold">Enter</span>
			<span class="font-dim">sends</span>
			<span class="font-dim">|</span>
			<span class="text-cyan font-bold">Esc</span>
			<span class="font-dim">closes</span>
		</div>
	} else if a.renActive.Get() {
		<div class="flex gap-1 shrink-0 px-1">
			<span class="text-cyan font-bold">{fmt.Sprintf("Rename %s to:", a.currentName())}</span>
			<span>{a.renText.Get()}</span>
			<span class="text-cyan blink">_</span>
			if a.renMsg.Get() != "" {
				<span class="text-red">{a.renMsg.Get()}</span>
			}
			<span class="text-cyan font-bold">Enter</span>
			<span class="font-dim">applies</span>
			<span class="font-dim">|</span>
			<span class="text-cyan font-bold">Esc</span>
			<span class="font-dim">cancels</span>
		</div>
	} else if a.memActive.Get() {
		<div class="flex gap-1 shrink-0 px-1">
			<span class="text-cyan font-bold">{fmt.Sprintf("Memory (MB) for %s:", a.currentName())}</span>
			<span>{a.memText.Get()}</span>
			<span class="text-cyan blink">_</span>
			if a.memMsg.Get() != "" {
				<span class="text-red">{a.memMsg.Get()}</span>
			}
			<span class="text-cyan font-bold">Enter</span>
			<span class="font-dim">applies on next start</span>
			<span class="font-dim">|</span>
			<span class="text-cyan font-bold">Esc</span>
			<span class="font-dim">cancels</span>
		</div>
	} else if a.delTarget.Get() != "" {
		<div class="flex gap-1 shrink-0 px-1">
			<span class="text-red font-bold">{fmt.Sprintf("Delete instance %q and ALL its files (worlds included)?", a.delTarget.Get())}</span>
			<span class="text-red font-bold">y</span>
			<span class="font-dim">delete</span>
			<span class="font-dim">|</span>
			<span class="text-red font-bold">n/Esc</span>
			<span class="font-dim">keep</span>
		</div>
	} else {
		@HintsRow(a.mainHints(), false)
	}
}

templ (a *app) Render() {
	if a.splash.Get() {
		@SplashView(a)
	} else {
		<div class="flex-col h-full p-1 gap-1">
			<div class="flex justify-between shrink-0">
				<span class="font-bold text-cyan">mc-tui-server</span>
				<span class="font-dim">{fmt.Sprintf("%d instances", len(a.managers.Get()))}</span>
			</div>
			<div class="flex gap-1 flex-grow">
				@Sidebar(a)
				if a.wizStep.Get() != wizOff {
					@WizardView(a)
				} else if a.fmOpen.Get() {
					@FilesView(a)
				} else if a.mrOpen.Get() {
					@ModrinthView(a)
				} else {
					@ConsoleView(a)
				}
			</div>
			@FooterBar(a)
		</div>
	}
}
