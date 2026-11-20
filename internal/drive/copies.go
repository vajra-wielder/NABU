package drive

import (
	"path/filepath"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/sysutil"
)

// OtherCopies finds the same dump as f on the other drives attached
// (another NABU drive, made with "Make a copy" say), to repair f from.
// The folder self (this drive's wikipedia folder) is left out.
func OtherCopies(f *dump.Files, self string) []*dump.Files {
	var out []*dump.Files
	seen := map[string]bool{}
	selfAbs, _ := filepath.Abs(self)
	for _, d := range sysutil.Drives() {
		cands := []string{filepath.Join(d.Path, Wikipedia)}
		for _, pat := range []string{"*", filepath.Join("*", "*")} {
			m, _ := filepath.Glob(filepath.Join(d.Path, pat, Wikipedia))
			cands = append(cands, m...)
		}
		for _, c := range cands {
			abs, _ := filepath.Abs(c)
			if abs == selfAbs || seen[abs] {
				continue
			}
			seen[abs] = true
			eds, _ := dump.Scan(c)
			for _, e := range eds {
				if dump.SameDump(f, e) {
					out = append(out, e)
				}
			}
		}
	}
	return out
}
