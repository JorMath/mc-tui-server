package main

import tui "github.com/grindlemire/go-tui"

// HelpView lista todos los atajos por contexto.
templ HelpView(a *app) {
	<div
		class="flex-col border-rounded border-cyan p-1 flex-grow"
		scrollable={tui.ScrollVertical}
		scrollOffset={0, 0}
	>
		<span class="font-bold text-cyan shrink-0">Keyboard shortcuts</span>
		for _, sec := range helpSections {
			<span class="font-bold shrink-0">{sec.Title}</span>
			for _, k := range sec.Keys {
				<div class="flex gap-1">
					<span class="text-cyan font-bold" minWidth={16}>{k.K}</span>
					<span class="font-dim">{k.L}</span>
				</div>
			}
		}
		<span class="font-dim shrink-0">Press Esc or ? to close</span>
	</div>
}
