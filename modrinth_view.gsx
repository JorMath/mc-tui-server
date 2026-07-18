package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// ModrinthView es el panel de búsqueda e instalación de Modrinth (R6).
templ ModrinthView(a *app) {
	<div class="flex-col border-rounded border-green p-1 flex-grow">
		<span class="font-bold text-green shrink-0">{fmt.Sprintf("Modrinth — %s", a.currentName())}</span>
		<div class="flex gap-1 shrink-0">
			<span class="text-green font-bold">Search:</span>
			<span>{a.mrQuery.Get()}</span>
			if a.mrTyping.Get() {
				<span class="text-green blink">_</span>
			}
		</div>
		<div
			class="flex-col flex-grow"
			scrollable={tui.ScrollVertical}
			scrollOffset={0, scrollTo(a.mrIdx.Get())}
		>
			for _, item := range a.mrItems() {
				if item.Sel {
					<span class="font-bold text-green">{fmt.Sprintf("> %s", item.Text)}</span>
				} else {
					<span>{fmt.Sprintf("  %s", item.Text)}</span>
				}
			}
		</div>
		if a.mrMsg.Get() != "" {
			<span class="text-yellow">{a.mrMsg.Get()}</span>
		}
		@HintsRow(a.mrHints(), true)
	</div>
}
