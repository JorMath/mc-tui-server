package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// IncidentView explains the most recent crash of the selected instance: the
// likely cause, where the evidence lives and the excerpt that supports it.
templ IncidentView(a *app) {
	<div class="flex-col border-rounded border-red p-1 flex-grow">
		<span class="font-bold text-red shrink-0">{fmt.Sprintf("Incident — %s", a.incName.Get())}</span>
		if a.incBusy.Get() {
			<span class="text-yellow shrink-0">{a.incStatusText()}</span>
		}
		if a.incFound.Get() {
			<span class="font-bold shrink-0">{fmt.Sprintf("Cause: %s", a.incReport.Get().Summary)}</span>
			if a.incReport.Get().Path != "" {
				<span class="font-dim shrink-0">{fmt.Sprintf("Report: %s", a.incReport.Get().Path)}</span>
			}
			if a.incReport.Get().LogPath != "" {
				<span class="font-dim shrink-0">{fmt.Sprintf("Log: %s", a.incReport.Get().LogPath)}</span>
			}
			<div
				class="flex-col flex-grow"
				scrollable={tui.ScrollVertical}
				scrollOffset={0, a.incScroll.Get()}
			>
				for _, line := range a.incReport.Get().Lines {
					<span>{line}</span>
				}
			</div>
		} else if !a.incBusy.Get() {
			if a.incMsg.Get() != "" {
				<span class="text-red">{a.incMsg.Get()}</span>
			} else {
				<span class="font-dim">No crash report or log found for this instance.</span>
			}
		}
		if a.incConfirm.Get() {
			<span class="text-yellow">{incUploadWarning}</span>
		} else if a.incURL.Get() != "" {
			<span class="text-green">{fmt.Sprintf("Uploaded: %s", a.incURL.Get())}</span>
		} else if a.incFound.Get() && !a.incBusy.Get() && a.incMsg.Get() != "" {
			<span class="text-red">{a.incMsg.Get()}</span>
		}
		@HintsRow(a.incHints(), true)
	</div>
}
