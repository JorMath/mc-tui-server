package main

import (
	"fmt"

	tui "github.com/grindlemire/go-tui"
)

// WizardView es el panel del asistente de nueva instancia (R4).
templ WizardView(a *app) {
	<div class="flex-col border-rounded border-cyan p-1 flex-grow">
		<span class="font-bold text-cyan shrink-0">{fmt.Sprintf("New instance — %s", a.wizStepTitle())}</span>
		if a.wizStep.Get() == wizType {
			for _, item := range a.wizTypeItems() {
				if item.Sel {
					<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
				} else {
					<span>{fmt.Sprintf("  %s", item.Text)}</span>
				}
			}
		}
		if a.wizStep.Get() == wizLoading {
			<span class="text-yellow">{a.wizMsg.Get()}</span>
		}
		if a.wizStep.Get() == wizVersion {
			<div
				class="flex-col flex-grow"
				scrollable={tui.ScrollVertical}
				scrollOffset={0, scrollTo(a.wizVerIdx.Get())}
			>
				for _, item := range a.wizVersionItems() {
					if item.Sel {
						<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
					} else {
						<span>{fmt.Sprintf("  %s", item.Text)}</span>
					}
				}
			</div>
		}
		if a.wizStep.Get() == wizPackSearch {
			<div class="flex gap-1">
				<span class="text-cyan font-bold">Search modpacks:</span>
				<span>{a.wizPackQuery.Get()}</span>
				<span class="text-cyan blink">_</span>
			</div>
			<span class="font-dim">Server-side modpacks: Fabric, Forge, NeoForge and Quilt</span>
			if a.wizMsg.Get() != "" {
				<span class="text-yellow">{a.wizMsg.Get()}</span>
			}
		}
		if a.wizStep.Get() == wizPackList {
			<div
				class="flex-col flex-grow"
				scrollable={tui.ScrollVertical}
				scrollOffset={0, scrollTo(a.wizPackIdx.Get())}
			>
				for _, item := range a.wizPackItems() {
					if item.Sel {
						<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
					} else {
						<span>{fmt.Sprintf("  %s", item.Text)}</span>
					}
				}
			</div>
		}
		if a.wizStep.Get() == wizPackVer {
			<div
				class="flex-col flex-grow"
				scrollable={tui.ScrollVertical}
				scrollOffset={0, scrollTo(a.wizPackVerIdx.Get())}
			>
				for _, item := range a.wizPackVerItems() {
					if item.Sel {
						<span class="font-bold text-cyan">{fmt.Sprintf("> %s", item.Text)}</span>
					} else {
						<span>{fmt.Sprintf("  %s", item.Text)}</span>
					}
				}
			</div>
		}
		if a.wizStep.Get() == wizName {
			<div class="flex gap-1">
				<span class="text-cyan font-bold">Name:</span>
				<span>{a.wizName.Get()}</span>
				<span class="text-cyan blink">_</span>
			</div>
			if a.wizMsg.Get() != "" {
				<span class="text-red">{a.wizMsg.Get()}</span>
			}
		}
		if a.wizStep.Get() == wizMem {
			<div class="flex gap-1">
				<span class="text-cyan font-bold">Memory (MB):</span>
				<span>{a.wizMemory.Get()}</span>
				<span class="text-cyan blink">_</span>
			</div>
		}
		if a.wizStep.Get() == wizEula {
			<span>To run the server you must accept the Minecraft EULA:</span>
			<span class="text-cyan">{"https://aka.ms/MinecraftEULA"}</span>
			<span class="font-bold">Do you accept?</span>
		}
		if a.wizStep.Get() == wizDownload {
			<span class="text-yellow">{a.wizMsg.Get()}</span>
		}
		if a.wizStep.Get() == wizError {
			<span class="text-red">{a.wizMsg.Get()}</span>
		}
		@HintsRow(a.wizHints(), false)
	</div>
}
