package main

// SplashView pinta el bloque de césped y el título en bloques sólidos.
// Los colores hex deben ser clases LITERALES (go-tui ignora class dinámica).
templ SplashView() {
	<div class="flex-col h-full items-center justify-center gap-1">
		<div class="flex-col">
			for _, row := range splashLogoRows() {
				<div class="flex">
					for _, seg := range row {
						if seg.Key == "g" {
							<span class="text-[#7cc65c]">{seg.Text}</span>
						} else if seg.Key == "G" {
							<span class="text-[#4a9e31]">{seg.Text}</span>
						} else if seg.Key == "d" {
							<span class="text-[#5c3d24]">{seg.Text}</span>
						} else if seg.Key == "b" {
							<span class="text-[#8b6244]">{seg.Text}</span>
						} else if seg.Key == "t" {
							<span class="text-[#a0764c]">{seg.Text}</span>
						} else {
							<span class="text-[#9a8f8a]">{seg.Text}</span>
						}
					}
				</div>
			}
		</div>
		<div class="flex-col">
			for _, line := range splashTitle {
				<span class="text-[#7cc65c]">{line}</span>
			}
		</div>
		<span class="font-dim">Press any key to start</span>
	</div>
}
