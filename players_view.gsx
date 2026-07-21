package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// PlayersView es el panel de jugadores (v0.3.0): whitelist, ops y bans.
templ PlayersView(a *app) {
	<div class="flex-col border-rounded border-cyan p-1 flex-grow">
		<span class="font-bold text-cyan shrink-0">{fmt.Sprintf("Players — %s · %s", a.currentName(), plTabsInfo[a.plTab.Get()].Title)}</span>
		<div class="flex gap-1 shrink-0">
			<span class="text-cyan font-bold">1</span>
			if a.plTab.Get() == 0 {
				<span class="text-cyan">Whitelist</span>
			} else {
				<span class="font-dim">Whitelist</span>
			}
			<span class="text-cyan font-bold">2</span>
			if a.plTab.Get() == 1 {
				<span class="text-cyan">Ops</span>
			} else {
				<span class="font-dim">Ops</span>
			}
			<span class="text-cyan font-bold">3</span>
			if a.plTab.Get() == 2 {
				<span class="text-cyan">Banned</span>
			} else {
				<span class="font-dim">Banned</span>
			}
		</div>
		if len(a.plItems()) == 0 {
			<span class="font-dim">(empty)</span>
		}
		<div
			class="flex-col flex-grow"
			scrollable={tui.ScrollVertical}
			scrollOffset={0, scrollTo(a.plIdx.Get())}
		>
			for _, item := range a.plItems() {
				if item.Sel {
					<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
				} else {
					<span>{fmt.Sprintf("  %s", item.Text)}</span>
				}
			}
		</div>
		if a.plAdding.Get() {
			<div class="flex gap-1">
				<span class="text-cyan font-bold">Player name:</span>
				<span>{a.plText.Get()}</span>
				<span class="text-cyan blink">_</span>
			</div>
		}
		if a.plMsg.Get() != "" {
			<span class="text-yellow">{a.plMsg.Get()}</span>
		}
		@HintsRow(a.plHints(), false)
	</div>
}
