// Package essentials is a ready-made set of collections: the English
// Wikipedia articles worth having first when there is no help to call
// (first aid, medicine, water, food, farming, shelter, tools, navigation
// and radio, energy, natural dangers).
package essentials

import (
	_ "embed"
	"strings"
)

//go:embed essentials.txt
var list string

// Prefix starts the name of every Essentials collection.
const Prefix = "Essentials: "

// Group is one collection.
type Group struct {
	Name   string // without Prefix
	Titles []string
}

// Groups lists the collections, in order.
func Groups() []Group {
	var out []Group
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "## "):
			out = append(out, Group{Name: strings.TrimSpace(line[3:])})
		case line == "" || strings.HasPrefix(line, "#"):
		case len(out) > 0:
			g := &out[len(out)-1]
			g.Titles = append(g.Titles, line)
		}
	}
	return out
}

// Count is how many articles there are in all.
func Count() int {
	n := 0
	for _, g := range Groups() {
		n += len(g.Titles)
	}
	return n
}
