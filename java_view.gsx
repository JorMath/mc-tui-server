package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// JavaView es el modal de Java por instancia (F2): Java del sistema,
// runtime gestionado de Temurin o ruta manual.
templ JavaView(a *app) {
	<div class="flex-col border-rounded border-cyan p-1 flex-grow">
		<span class="font-bold text-cyan">{fmt.Sprintf("Java runtime — %s", a.javaTarget)}</span>
		<div class="flex gap-1 shrink-0">
			<span class="font-dim">Minecraft:</span>
			<span>{a.javaVersionLabel()}</span>
			<span class="font-dim">| Needs:</span>
			<span>{a.javaNeedLabel()}</span>
			<span class="font-dim">| Current:</span>
			<span>{a.javaCurrentLabel()}</span>
		</div>
		if a.javaBusy.Get() {
			<span class="text-yellow">{a.javaMsg.Get()}</span>
			<span class="font-dim">Esc cancels the download</span>
		} else if a.javaTyping.Get() {
			<div class="flex gap-1 shrink-0">
				<span class="text-cyan font-bold">Java path:</span>
				<span>{a.javaPath.Get()}</span>
				<span class="text-cyan blink">_</span>
			</div>
			if a.javaMsg.Get() != "" {
				<span class="text-yellow">{a.javaMsg.Get()}</span>
			}
		} else {
			<span class="font-bold text-cyan">Choose the Java for this instance:</span>
			for _, item := range a.javaItems() {
				if item.Sel {
					<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
				} else {
					<span>{fmt.Sprintf("  %s", item.Text)}</span>
				}
			}
			if a.javaMsg.Get() != "" {
				<span class="text-yellow">{a.javaMsg.Get()}</span>
			}
		}
		@HintsRow(a.javaHints(), true)
	</div>
}
