package server

import (
	"net/http"
	"path/filepath"
	"sort"

	"github.com/vajra-wielder/nabu/internal/share"
	"github.com/vajra-wielder/nabu/internal/sysutil"
)

type copyWiki struct {
	DB, Name                      string
	Dump, Index, Search, Pictures int64
}

type copyDrive struct {
	Path, Target string
	Free         int64
}

// serveCopy shows the "Make a copy" page.
func (s *Server) serveCopy(w http.ResponseWriter, r *http.Request) {
	parts, _ := share.Inventory(s.Lib.Drive)
	size := map[string]int64{}
	for _, p := range parts {
		size[p.Key] = p.Bytes
	}
	var wikis []copyWiki
	for _, p := range parts {
		if len(p.Key) > 5 && p.Key[:5] == "wiki:" {
			db := p.Key[5:]
			wikis = append(wikis, copyWiki{DB: db, Name: wikiName(db), Dump: p.Bytes, Index: size["index:"+db],
				Search: size["search:"+db], Pictures: size["pictures:"+db]})
		}
	}
	sort.Slice(wikis, func(i, j int) bool { return wikis[i].Dump > wikis[j].Dump })
	var drives []copyDrive
	for _, d := range sysutil.Drives() {
		if rel, err := filepath.Rel(d.Path, s.Lib.Drive.Root); err == nil && rel == "." {
			continue // the drive NABU runs from, when it is its root
		}
		drives = append(drives, copyDrive{Path: d.Path, Free: d.Free, Target: filepath.Join(d.Path, "NABU")})
	}
	s.render(w, r, "copy.html", map[string]interface{}{
		"PageTitle": "Make a copy", "Wikis": wikis, "Drives": drives, "Size": size,
		"Copy": s.copier.Status(), "Root": s.Lib.Drive.Root,
	})
}
