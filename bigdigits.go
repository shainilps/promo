package main

import "strings"

// 3x5 pixel glyphs; each pixel is drawn two cells wide.
var glyphs = map[rune][5]string{
	'0': {"###", "# #", "# #", "# #", "###"},
	'1': {"## ", " # ", " # ", " # ", "###"},
	'2': {"###", "  #", "###", "#  ", "###"},
	'3': {"###", "  #", "###", "  #", "###"},
	'4': {"# #", "# #", "###", "  #", "  #"},
	'5': {"###", "#  ", "###", "  #", "###"},
	'6': {"###", "#  ", "###", "# #", "###"},
	'7': {"###", "  #", "  #", "  #", "  #"},
	'8': {"###", "# #", "###", "# #", "###"},
	'9': {"###", "# #", "###", "  #", "###"},
	':': {" ", "#", " ", "#", " "},
}

// bigText renders digits and colons as 5-line block characters.
func bigText(s string) string {
	var rows [5]strings.Builder
	for i, r := range s {
		g, ok := glyphs[r]
		if !ok {
			continue
		}
		for row := range 5 {
			if i > 0 {
				rows[row].WriteString("  ")
			}
			for _, px := range g[row] {
				if px == '#' {
					rows[row].WriteString("██")
				} else {
					rows[row].WriteString("  ")
				}
			}
		}
	}
	lines := make([]string, 5)
	for i := range rows {
		lines[i] = rows[i].String()
	}
	return strings.Join(lines, "\n")
}

func bigTextWidth(s string) int {
	w := 0
	for i, r := range s {
		if i > 0 {
			w += 2
		}
		w += 2 * len(glyphs[r][0])
	}
	return w
}
