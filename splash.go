package main

// Arte ASCII del splash: solo bloques sólidos y espacios — los caracteres
// de caja (╔═╗) se superponen en algunas fuentes de terminal.
// splashFont: 5 filas por letra, ancho fijo por letra.
var splashFont = map[rune][]string{
	'M': {"█   █", "██ ██", "█ █ █", "█   █", "█   █"},
	'C': {" ███", "█   ", "█   ", "█   ", " ███"},
	'-': {"    ", "    ", " ██ ", "    ", "    "},
	'T': {"█████", "  █  ", "  █  ", "  █  ", "  █  "},
	'U': {"█   █", "█   █", "█   █", "█   █", " ███ "},
	'I': {"███", " █ ", " █ ", " █ ", "███"},
	'S': {" ████", "█    ", " ███ ", "    █", "████ "},
	'E': {"█████", "█    ", "███  ", "█    ", "█████"},
	'R': {"████ ", "█   █", "████ ", "█  █ ", "█   █"},
	'V': {"█   █", "█   █", "█   █", " █ █ ", "  █  "},
}

// blank es el "braille pattern blank" (U+2800): ocupa una celda pero no es
// espacio, así el layout no lo colapsa ni lo recorta.
const blank = "⠀"

// renderWord compone una palabra duplicando cada celda en horizontal
// (píxeles de 2 columnas). Los huecos usan blank en vez de espacios.
func renderWord(word string) []string {
	rows := make([]string, 5)
	for r := 0; r < 5; r++ {
		for i, ch := range word {
			if i > 0 {
				rows[r] += blank + blank
			}
			for _, c := range splashFont[ch][r] {
				if c == '█' {
					rows[r] += "██"
				} else {
					rows[r] += blank + blank
				}
			}
		}
	}
	return rows
}

var splashTitle = append(append(renderWord("MC-TUI"), blank), renderWord("SERVER")...)

// splashLogo es el bloque de césped de Minecraft en píxeles:
// g/G césped (verde claro/oscuro), b/t tierra (media/clara), d tierra
// oscura, s piedra gris. Cada píxel se pinta como "██" con color hex literal.
var splashLogo = []string{
	"ggGgggGggg",
	"GggGgggggG",
	"dgGdggdggd",
	"ddsddgddbd",
	"bdbbtdbdbb",
	"dbddbbdtdd",
	"bbdsddbbdb",
	"dtbdbddbbd",
	"bddbdsbddb",
}

type logoSeg struct {
	Text string
	Key  string
}

// splashLogoRows agrupa píxeles contiguos del mismo color en un solo
// segmento para no crear un span por píxel.
func splashLogoRows() [][]logoSeg {
	rows := make([][]logoSeg, len(splashLogo))
	for i, row := range splashLogo {
		var segs []logoSeg
		for _, c := range row {
			key := string(c)
			if n := len(segs); n > 0 && segs[n-1].Key == key {
				segs[n-1].Text += "██"
				continue
			}
			segs = append(segs, logoSeg{Text: "██", Key: key})
		}
		rows[i] = segs
	}
	return rows
}
