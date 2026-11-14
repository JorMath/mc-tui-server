package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// MenuView is the actions menu (F3): a short, grouped list of everything
// that is not a global shortcut. Disabled rows show their reason instead of
// running.
templ MenuView(a *app) {
	<div
		class="flex-col border-rounded border-cyan p-1 flex-grow"
		scrollable={tui.ScrollVertical}
		scrollOffset={0, a.menuScrollY()}
	>
		<span class="font-bold text-cyan shrink-0">{fmt.Sprintf("Actions — %s (%s)", a.currentName(), a.statusText(a.currentName()))}</span>
		for _, row := range a.menuRows() {
			if row.Heading != "" {
				<span class="font-dim shrink-0">{row.Heading}</span>
			}
			if row.Sel {
				if row.Item.Disabled {
					<div class="flex gap-1">
						<span class="font-bold text-yellow">{fmt.Sprintf("> %s", row.Item.Label)}</span>
						<span class="text-yellow">{fmt.Sprintf("— %s", row.Item.Reason)}</span>
					</div>
				} else {
					<div class="flex gap-1">
						<span class="font-bold text-cyan">{fmt.Sprintf("> %s", row.Item.Label)}</span>
						<span class="font-dim">{fmt.Sprintf("— %s", row.Item.Desc)}</span>
					</div>
				}
			} else if row.Item.Disabled {
				<span class="font-dim">{fmt.Sprintf("  %s — %s", row.Item.Label, row.Item.Desc)}</span>
			} else {
				<div class="flex gap-1">
					<span>{fmt.Sprintf("  %s", row.Item.Label)}</span>
					<span class="font-dim">{fmt.Sprintf("— %s", row.Item.Desc)}</span>
				</div>
			}
		}
		if a.menuMsg.Get() != "" {
			<span class="text-yellow">{a.menuMsg.Get()}</span>
		}
		@HintsRow(a.menuHints(), true)
	</div>
}
