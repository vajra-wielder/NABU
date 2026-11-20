// Package plan works out how much room a choice of NABU contents takes,
// so a drive's contents can be chosen (and tested) against its size
// before anything is downloaded. Sizes are those of the November 2026
// dumps and of the measurements in docs/PICTURES-SURVEY.md.
package plan

import (
	"fmt"
	"strings"

	"github.com/vajra-wielder/nabu/internal/pictures"
)

// Wiki is what NABU knows about a Wikipedia's size.
type Wiki struct {
	DB       string
	Name     string
	Bytes    int64 // the dump: text, needed
	Articles int64
}

// Wikis are the ten Wikipedias NABU offers, largest first.
var Wikis = []Wiki{
	{"enwiki", "English", 25_400e6, 7_266_259},
	{"frwiki", "French", 6_900e6, 2_790_246},
	{"ruwiki", "Russian", 5_900e6, 2_130_134},
	{"eswiki", "Spanish", 5_100e6, 2_150_721},
	{"zhwiki", "Chinese", 3_400e6, 1_566_794},
	{"ptwiki", "Portuguese", 2_700e6, 1_170_000},
	{"arwiki", "Arabic", 2_100e6, 1_340_029},
	{"fawiki", "Persian", 1_500e6, 1_050_000},
	{"idwiki", "Indonesian", 1_300e6, 805_141},
	{"dawiki", "Danish", 512e6, 316_786},
}

// Measured ratios to the dump's size.
const (
	TitleIndexShare = 0.026 // title index (indexes/), of the dump on the drive
	FullTextShare   = 0.125 // search inside articles (search/), of the dump as downloaded
	Programs        = 90e6  // the programs for Windows, Mac and Linux
)

// SlimShare is what is left of a dump on the drive (dump.Slim): without
// the pages NABU never shows, sampled on English Wikipedia (71% articles,
// 6% redirects, 2% categories, files and portals; 18% project
// discussions), and measured on Simple English for the others (86%);
// then 8% more for keeping articles in small, quick-to-open bundles
// (measured on Simple English: 341 to 368 MB).
var SlimShare = map[string]float64{"enwiki": 0.80 * 1.08}

const defaultSlimShare = 0.93

// OnDrive is the size of a wiki's dump on the drive, without the pages
// NABU never shows.
func (w Wiki) OnDrive() int64 {
	s, ok := SlimShare[w.DB]
	if !ok {
		s = defaultSlimShare
	}
	return int64(float64(w.Bytes) * s)
}

// Picture choices for one Wikipedia.
const (
	NoPictures      = ""        // pictures only show online
	EnglishPictures = "english" // show English's pictures, keep none of its own (the default for other languages)
	MainPictures    = pictures.Lead
	AllPictures     = pictures.All
)

// Choice is one Wikipedia on the drive.
type Choice struct {
	DB       string
	FullText bool
	Pictures string
}

// Plan is the contents of a drive.
type Plan struct {
	Choices      []Choice
	PictureLimit int64 // room set aside for pictures (0: none)
	UpdateRoom   bool  // keep room on the drive for the largest dump's next edition (only when updates cannot go through the computer)
}

// Line is one part of a plan.
type Line struct {
	What  string
	Bytes int64
}

// Find returns a Wikipedia by "enwiki", "en" or "English".
func Find(s string) (Wiki, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, w := range Wikis {
		if w.DB == s || strings.TrimSuffix(w.DB, "wiki") == s || strings.ToLower(w.Name) == s {
			return w, true
		}
	}
	return Wiki{}, false
}

// PictureBytes is the room a wiki's pictures take on their own (before
// any limit), with English's already on the drive when englishKept.
func PictureBytes(w Wiki, mode string, englishKept bool) int64 {
	lead, all := pictures.Estimate(w.DB, w.Articles, englishKept)
	switch mode {
	case MainPictures:
		return lead.Bytes
	case AllPictures:
		return all.Bytes
	}
	return 0
}

// Lines lists what the plan puts on the drive.
func (p Plan) Lines() []Line {
	out := []Line{{"Programs (Windows, Mac, Linux)", Programs}}
	english := false
	for _, c := range p.Choices {
		if c.DB == "enwiki" && (c.Pictures == MainPictures || c.Pictures == AllPictures) {
			english = true
		}
	}
	var pics, largest int64
	for _, c := range p.Choices {
		w, ok := Find(c.DB)
		if !ok {
			continue
		}
		out = append(out, Line{w.Name + ": text (articles only)", w.OnDrive()})
		out = append(out, Line{w.Name + ": title index", int64(float64(w.OnDrive()) * TitleIndexShare)})
		if c.FullText {
			out = append(out, Line{w.Name + ": search inside articles", int64(float64(w.Bytes) * FullTextShare)})
		}
		pics += PictureBytes(w, c.Pictures, english && w.DB != "enwiki")
		if w.OnDrive() > largest {
			largest = w.OnDrive()
		}
	}
	if pics > 0 {
		what := "Pictures"
		if p.PictureLimit > 0 && pics > p.PictureLimit {
			pics, what = p.PictureLimit, "Pictures (up to the limit set aside; longest articles first)"
		}
		out = append(out, Line{what, pics})
	}
	if p.UpdateRoom && largest > 0 {
		out = append(out, Line{"Room to download the next edition of the largest Wikipedia", int64(float64(largest) * (1 + TitleIndexShare))})
	}
	return out
}

// Total is the room the whole plan takes.
func (p Plan) Total() int64 {
	var n int64
	for _, l := range p.Lines() {
		n += l.Bytes
	}
	return n
}

// Default is NABU's recommended contents for a drive of the given size:
// English with search inside articles and its main pictures, and second
// (if any) showing English's pictures. Updates go through the computer's
// own disk, so the drive keeps no room spare for them. Pictures get what
// is left (rounded down to 5 GB, keeping 5% of the drive spare).
func Default(second string, driveBytes int64) Plan {
	p := Plan{Choices: []Choice{{DB: "enwiki", FullText: true, Pictures: MainPictures}}}
	if w, ok := Find(second); ok && w.DB != "enwiki" {
		p.Choices = append(p.Choices, Choice{DB: w.DB, Pictures: EnglishPictures})
	}
	without := Plan{Choices: []Choice{p.Choices[0]}}
	without.Choices[0].Pictures = NoPictures
	without.Choices = append(without.Choices, p.Choices[1:]...)
	rest := driveBytes - driveBytes/20 - without.Total()
	p.PictureLimit = rest / 5e9 * 5e9
	if p.PictureLimit < 0 {
		p.PictureLimit = 0
	}
	return p
}

// String is the plan as a table.
func (p Plan) String() string {
	var b strings.Builder
	for _, l := range p.Lines() {
		fmt.Fprintf(&b, "  %-62s %8.1f GB\n", l.What, float64(l.Bytes)/1e9)
	}
	fmt.Fprintf(&b, "  %-62s %8.1f GB\n", "Total", float64(p.Total())/1e9)
	return b.String()
}
