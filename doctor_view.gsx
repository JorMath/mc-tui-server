package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// DoctorView lists the pre-flight checks of the selected instance: one row
// per check with its status, name and the reason when it fails.
templ DoctorView(a *app) {
	<div class="flex-col border-rounded border-cyan p-1 flex-grow">
		<span class="font-bold text-cyan shrink-0">{fmt.Sprintf("Doctor — %s", a.docName.Get())}</span>
		if a.docBusy.Get() {
			<span class="text-yellow shrink-0">Running checks...</span>
		}
		if a.docMsg.Get() != "" {
			<span class="text-red shrink-0">{a.docMsg.Get()}</span>
		}
		<div
			class="flex-col flex-grow"
			scrollable={tui.ScrollVertical}
			scrollOffset={0, a.docScroll.Get()}
		>
			for _, check := range a.docChecks.Get() {
				<div class="flex gap-1">
					if check.OK {
						<span class="text-green font-bold">✓</span>
					} else {
						<span class="text-red font-bold">✗</span>
					}
					<span class="font-bold">{check.Name}</span>
					<span class="font-dim">{check.Detail}</span>
				</div>
			}
		</div>
		@HintsRow(a.docHints(), true)
	</div>
}
