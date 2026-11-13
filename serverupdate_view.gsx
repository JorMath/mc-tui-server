package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// ServerUpdateView es el panel de actualización del software del servidor
// (F1): lista las versiones que ofrece el provider, más nuevas primero.
templ ServerUpdateView(a *app) {
	<div class="flex-col border-rounded border-green p-1 flex-grow">
		<span class="font-bold text-green shrink-0">{fmt.Sprintf("Update server — %s", a.currentName())}</span>
		<div class="flex gap-1 shrink-0">
			<span class="font-dim">Type:</span>
			<span>{a.suTypeLabel()}</span>
			<span class="font-dim">| Installed:</span>
			<span>{a.suCurrentLabel()}</span>
			if a.suBusy.Get() {
				<span class="text-yellow blink">working...</span>
			}
		</div>
		<div
			class="flex-col flex-grow"
			scrollable={tui.ScrollVertical}
			scrollOffset={0, scrollTo(a.suIdx.Get())}
		>
			if len(a.suVersions.Get()) == 0 {
				<span class="font-dim">{a.suMsg.Get()}</span>
			}
			for i, version := range a.suVersions.Get() {
				if i == a.suIdx.Get() {
					<span class="font-bold text-green">{fmt.Sprintf("> %s%s", version, a.suTag(version))}</span>
				} else {
					<span>{fmt.Sprintf("  %s%s", version, a.suTag(version))}</span>
				}
			}
		</div>
		if a.suMsg.Get() != "" && len(a.suVersions.Get()) > 0 {
			<span class="text-yellow">{a.suMsg.Get()}</span>
		}
		if a.suConfirm.Get() != "" {
			<div class="flex gap-1 shrink-0">
				<span class="text-yellow font-bold">{a.suConfirm.Get()}</span>
				<span class="text-yellow font-bold">y</span>
				<span class="font-dim">update</span>
				<span class="font-dim">|</span>
				<span class="text-yellow font-bold">n/Esc</span>
				<span class="font-dim">cancel</span>
			</div>
		} else if a.suRestart.Get() {
			<div class="flex gap-1 shrink-0">
				<span class="text-green font-bold">Restart the server now?</span>
				<span class="text-green font-bold">y</span>
				<span class="font-dim">restart</span>
				<span class="font-dim">|</span>
				<span class="text-green font-bold">n/Esc</span>
				<span class="font-dim">later</span>
			</div>
		} else {
			@HintsRow(a.suHints(), true)
		}
	</div>
}
