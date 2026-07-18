package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// FilesView es el panel de archivos (R3): properties, mundos y plugins.
templ FilesView(a *app) {
	<div class="flex-col border-rounded border-cyan p-1 flex-grow">
		<span class="font-bold text-cyan shrink-0">{a.fmTitle()}</span>
		<div class="flex gap-1 shrink-0">
			<span class="text-cyan font-bold">1</span>
			if a.fmTab.Get() == 0 {
				<span class="text-cyan">Properties</span>
			} else {
				<span class="font-dim">Properties</span>
			}
			<span class="text-cyan font-bold">2</span>
			if a.fmTab.Get() == 1 {
				<span class="text-cyan">Worlds</span>
			} else {
				<span class="font-dim">Worlds</span>
			}
			<span class="text-cyan font-bold">3</span>
			if a.fmTab.Get() == 2 {
				<span class="text-cyan">Plugins/Mods</span>
			} else {
				<span class="font-dim">Plugins/Mods</span>
			}
		</div>
		if len(a.fmItems()) == 0 {
			<span class="font-dim">(empty)</span>
		}
		<div
			class="flex-col flex-grow"
			scrollable={tui.ScrollVertical}
			scrollOffset={0, a.fmScrollY()}
		>
			for _, item := range a.fmItems() {
				if item.Sel {
					<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
				} else {
					<span>{fmt.Sprintf("  %s", item.Text)}</span>
				}
			}
		</div>
		if a.fmEditing.Get() {
			<div class="flex gap-1">
				<span class="text-cyan font-bold">{fmt.Sprintf("%s =", a.fmSelectedKey())}</span>
				<span>{a.fmEditText.Get()}</span>
				<span class="text-cyan blink">_</span>
				<span class="font-dim">(Enter applies | Esc cancels)</span>
			</div>
		}
		if a.fmConfirm.Get() != "" {
			<span class="text-red font-bold">{fmt.Sprintf("Delete %q permanently? (y = yes, n = no)", a.fmConfirm.Get())}</span>
		}
		if a.fmMsg.Get() != "" {
			<span class="text-yellow">{a.fmMsg.Get()}</span>
		}
		@HintsRow(a.fmHints(), false)
	</div>
}
