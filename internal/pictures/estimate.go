package pictures

// Survey is what a sample of a wiki's articles showed about pictures
// (docs/PICTURES-SURVEY.md, from tools/picture-survey).
type Survey struct {
	LeadShare   float64 // share of articles with at least one picture
	PerArticle  float64 // distinct pictures per article, on average
	LeadBytes   float64 // average size of a main picture at 250 px
	OtherBytes  float64 // average size of the other pictures at 250 px
	SharedShare float64 // share of pictures used by more than one article (not counted twice)
	OwnShare    float64 // share that are the wiki's own files, not on Commons
	AlsoEnglish float64 // share of its Commons pictures that English articles use too
}

// surveys were measured in November 2026 (docs/PICTURES-SURVEY.md).
// Picture sizes are pooled over all ten wikis: about fifteen of each per
// wiki is too few to tell them apart. defaultSurvey is for any other wiki.
var surveys = map[string]Survey{
	"enwiki": {LeadShare: 0.453, PerArticle: 0.963, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.073, OwnShare: 0.080},
	"ruwiki": {LeadShare: 0.255, PerArticle: 1.008, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.047, OwnShare: 0.048, AlsoEnglish: 0.48},
	"arwiki": {LeadShare: 0.200, PerArticle: 0.521, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.031, OwnShare: 0.030, AlsoEnglish: 0.69},
	"eswiki": {LeadShare: 0.410, PerArticle: 1.000, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.070, OwnShare: 0.000, AlsoEnglish: 0.60},
	"ptwiki": {LeadShare: 0.510, PerArticle: 1.065, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.055, OwnShare: 0.080, AlsoEnglish: 0.61},
	"zhwiki": {LeadShare: 0.392, PerArticle: 1.115, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.092, OwnShare: 0.037, AlsoEnglish: 0.67},
	"frwiki": {LeadShare: 0.439, PerArticle: 1.266, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.062, OwnShare: 0.047, AlsoEnglish: 0.48},
	"fawiki": {LeadShare: 0.574, PerArticle: 1.006, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.099, OwnShare: 0.174, AlsoEnglish: 0.72},
	"idwiki": {LeadShare: 0.489, PerArticle: 1.232, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.092, OwnShare: 0.113, AlsoEnglish: 0.64},
	"dawiki": {LeadShare: 0.466, PerArticle: 1.146, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.054, OwnShare: 0.000, AlsoEnglish: 0.56},
}

var defaultSurvey = Survey{LeadShare: 0.45, PerArticle: 1.05, LeadBytes: 24800, OtherBytes: 31000, SharedShare: 0.07, OwnShare: 0.06, AlsoEnglish: 0.6}

// PerHour is how many pictures NABU keeps in an hour within Wikimedia's
// limits on an ordinary connection (2 at a time).
const PerHour = 20000

// Size is an estimate for a choice of pictures.
type Size struct {
	Pictures int64
	Bytes    int64
	Hours    float64
}

// Estimate guesses what keeping a wiki's pictures takes: the main
// picture of each article (lead) or every picture (all). With English's
// pictures already on the drive, the Commons pictures English uses too
// are not downloaded again, and the estimate leaves them out.
func Estimate(wiki string, articles int64, englishKept bool) (lead, all Size) {
	sv, ok := surveys[wiki]
	if !ok {
		sv = defaultSurvey
	}
	defer func() {
		if englishKept && wiki != "enwiki" {
			f := sv.OwnShare + (1-sv.OwnShare)*(1-sv.AlsoEnglish)
			scale := func(x *Size) {
				x.Pictures = int64(float64(x.Pictures) * f)
				x.Bytes = int64(float64(x.Bytes) * f)
				x.Hours *= f
			}
			scale(&lead)
			scale(&all)
		}
	}()
	a := float64(articles)
	lp := a * sv.LeadShare
	ap := a * sv.PerArticle * (1 - sv.SharedShare)
	if ap < lp {
		ap = lp
	}
	// Survey sizes are as Wikimedia sends pictures; NABU keeps them
	// smaller (Shrink).
	lead = Size{Pictures: int64(lp), Bytes: int64(lp * sv.LeadBytes * ShrinkFactor), Hours: lp / PerHour}
	all = Size{Pictures: int64(ap), Bytes: int64((lp*sv.LeadBytes + (ap-lp)*sv.OtherBytes) * ShrinkFactor), Hours: ap / PerHour}
	return lead, all
}
