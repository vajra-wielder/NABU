package plan

import (
	"testing"

	"github.com/vajra-wielder/nabu/internal/update"
)

// The drive NABU is made for: 125 GB.
const drive = 125e9

// English plus any one other Wikipedia, the way NABU sets them up by
// default, fits the drive with room for English's next update and a
// little to spare.
func TestDefaultFits125GB(t *testing.T) {
	for _, w := range Wikis {
		p := Plan{UpdateRoom: true, PictureLimit: update.DefaultPictureLimitGB * 1e9, Choices: []Choice{
			{DB: "enwiki", FullText: true, Pictures: MainPictures},
			{DB: w.DB, Pictures: EnglishPictures},
		}}
		if w.DB == "enwiki" {
			p.Choices = p.Choices[:1]
		}
		if total := p.Total(); total > drive-drive/20 {
			t.Errorf("English + %s takes %.1f GB, more than 95%% of 125 GB:\n%s", w.Name, float64(total)/1e9, p)
		}
	}
	// The default limit is what Default works out for that drive.
	if d := Default("fr", drive); d.PictureLimit != update.DefaultPictureLimitGB*1e9 {
		t.Errorf("default picture limit %d GB; Default works out %.0f GB:\n%s",
			update.DefaultPictureLimitGB, float64(d.PictureLimit)/1e9, d)
	}
}

// English's main pictures, kept small, fit the room set aside, so the
// limit only matters for "all pictures" or more languages.
func TestEnglishMainPicturesFitTheLimit(t *testing.T) {
	en, _ := Find("en")
	if b := PictureBytes(en, MainPictures, false); b > update.DefaultPictureLimitGB*1e9 {
		t.Errorf("English main pictures %.1f GB", float64(b)/1e9)
	}
	if b := PictureBytes(en, AllPictures, false); b < update.DefaultPictureLimitGB*1e9 {
		t.Errorf("English all pictures %.1f GB: the limit should matter for them", float64(b)/1e9)
	}
}

// A second language showing English's pictures takes no room for them.
func TestEnglishPicturesCostNothing(t *testing.T) {
	a := Plan{Choices: []Choice{{DB: "enwiki"}, {DB: "frwiki", Pictures: EnglishPictures}}}
	b := Plan{Choices: []Choice{{DB: "enwiki"}, {DB: "frwiki"}}}
	if a.Total() != b.Total() {
		t.Fatalf("%d != %d", a.Total(), b.Total())
	}
	c := Plan{Choices: []Choice{{DB: "enwiki", Pictures: MainPictures}, {DB: "frwiki", Pictures: MainPictures}}}
	d := Plan{Choices: []Choice{{DB: "frwiki", Pictures: MainPictures}}}
	en, _ := Find("en")
	fr, _ := Find("fr")
	if c.Total()-PictureBytes(en, MainPictures, false) >= d.Total()+en.Bytes*103/100+1 {
		t.Fatal("French's own pictures should be fewer once English's are kept")
	}
	if PictureBytes(fr, MainPictures, true) >= PictureBytes(fr, MainPictures, false) {
		t.Fatal("no saving from English's pictures")
	}
}

func TestFind(t *testing.T) {
	for _, s := range []string{"fr", "frwiki", "French", " FRENCH "} {
		if w, ok := Find(s); !ok || w.DB != "frwiki" {
			t.Errorf("%q", s)
		}
	}
	if _, ok := Find("xx"); ok {
		t.Error("xx found")
	}
}
