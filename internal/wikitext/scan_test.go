package wikitext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/testfolder"
)

// The hand-written scanners do what the regular expressions did.
func checkSame(t *testing.T, src string) {
	t.Helper()
	for _, c := range []struct {
		name      string
		got, want string
	}{
		{"comments", dropComments(src), commentRe.ReplaceAllString(src, "")},
		{"refs", dropRefs(src), refBlockRe.ReplaceAllString(src, " ")},
		{"urls", dropBareURLs(src), bareURLText.ReplaceAllString(src, " ")},
		{"table marks", dropTableMarks(src), tableMarkRe.ReplaceAllString(src, " ")},
		{"cell attributes", dropCellAttrs(src), cellAttrRe.ReplaceAllString(src, "$1 ")},
		{"heading marks", dropHeadingMarks(src), headEqRe.ReplaceAllString(src, " ")},
	} {
		if c.got != c.want {
			i := 0
			for i < len(c.got) && i < len(c.want) && c.got[i] == c.want[i] {
				i++
			}
			from := i - 60
			if from < 0 {
				from = 0
			}
			end := func(s string) string {
				if i+60 < len(s) {
					return s[from : i+60]
				}
				return s[from:]
			}
			t.Fatalf("%s differs at %d:\n got  %q\n want %q", c.name, i, end(c.got), end(c.want))
		}
	}
}

func TestScannersMatchRegexps(t *testing.T) {
	for _, src := range []string{
		"", "plain", "a<!-- c -->b<!-- open", "x<ref>a</ref>y<REF name=\"n\"/>z<ref name=a/b>q</ref >w<references/>",
		"<ref>never closed", "<ref name=x>a</refx> b </ref> c", "see http://x.org/a b https://y ftp:// z http://",
		"{| class=wikitable\n|-\n! a !! b\n|+ cap\n| c || d\n|}\n  \n  {|x\n|}tail\n",
		"|style=\"x\"| a || align=\"left\" | b\n! scope=\"col\"|h\n|  colspan=\"2\" bad\n",
		"== H ==\ntext = x\n=== D ===  \n\n== E\n=\n", "a ==\n\n\nb", "==",
	} {
		checkSame(t, src)
	}
}

// On real articles (NABU_REAL_DUMPS or NABU_STUDY: a folder with dumps).
func TestScannersOnRealArticles(t *testing.T) {
	dir := os.Getenv("NABU_REAL_DUMPS")
	if dir == "" {
		if d := os.Getenv("NABU_STUDY"); d != "" {
			dir = filepath.Join(d, "wikipedia")
		}
	}
	if dir == "" && testfolder.Asked("ScannersOnReal") {
		if f := testfolder.ForTests(); f != "" {
			dir = filepath.Join(f, "wikipedia")
		}
	}
	if dir == "" {
		t.Skip("run with -run ScannersOnReal (uses the NABU-test folder), or set NABU_REAL_DUMPS")
	}
	eds, _ := dump.Scan(dir)
	n := 0
	for _, f := range eds {
		ix, err := dump.OpenIndex(filepath.Join(filepath.Dir(dir), "indexes", f.Key()+".nidx"), f)
		if err != nil {
			t.Skip(err)
		}
		rd, err := dump.NewReader(f, ix)
		if err != nil {
			t.Fatal(err)
		}
		total := ix.StreamCount()
		for i := 0; i < 400; i++ {
			ps, err := rd.StreamPages(uint32(i * total / 400))
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range ps {
				checkSame(t, p.Text)
				// and the steps after templates and links, as PlainText meets them
				checkSame(t, strings.ReplaceAll(p.Text, "{{", "{"))
				n++
			}
		}
		rd.Close()
	}
	t.Logf("%d articles the same", n)
}
