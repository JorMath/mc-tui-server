package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// FilesView es el panel de archivos (R3): properties, mundos, plugins,
// backups y logs. tabRefs permite cambiar de pestaña con el ratón.
templ FilesView(a *app, tabRefs *tui.RefMap[string]) {
	<div class="flex-col border-rounded border-cyan p-1 flex-grow">
		<span class="font-bold text-cyan shrink-0">{a.fmTitle()}</span>
		<div class="flex gap-1 shrink-0">
			for i, name := range fmTabNames {
				<div class="flex gap-1" ref={tabRefs} key={name}>
					<span class="text-cyan font-bold">{fmt.Sprintf("%d", i+1)}</span>
					if a.fmTab.Get() == i {
						<span class="text-cyan">{name}</span>
					} else {
						<span class="font-dim">{name}</span>
					}
				</div>
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
		if a.fmRestore.Get() != "" {
			<span class="text-red font-bold">{fmt.Sprintf("Restore %q? The current world will be REPLACED (y = yes, n = no)", a.fmRestore.Get())}</span>
		}
		if a.fmMsg.Get() != "" {
			<span class="text-yellow">{a.fmMsg.Get()}</span>
		}
		@HintsRow(a.fmHints(), false)
	</div>
}
