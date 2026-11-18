package pictures

// Survey is what a sample of a wiki's articles showed about pictures
// (docs/PICTURES-SURVEY.md, from tools/picture-survey).
type Survey struct {
	LeadShare   float64 // share of articles with at least one picture
	PerArticle  float64 // distinct pictures per article, on average
	LeadBytes   float64 // average size of a main picture at 250 px
	OtherBytes  float64 // average size of the other pictures at 250 px
	SharedShare float64 // share of pictures used by more than one article (not counted twice)
}

// surveys are filled in from the survey; defaultSurvey is for any other
// wiki.
var surveys = map[string]Survey{}

var defaultSurvey = Survey{LeadShare: 0.5, PerArticle: 2.0, LeadBytes: 22000, OtherBytes: 16000, SharedShare: 0.2}

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
// picture of each article (lead) or every picture (all).
func Estimate(wiki string, articles int64) (lead, all Size) {
	sv, ok := surveys[wiki]
	if !ok {
		sv = defaultSurvey
	}
	a := float64(articles)
	lp := a * sv.LeadShare
	ap := a * sv.PerArticle * (1 - sv.SharedShare)
	if ap < lp {
		ap = lp
	}
	lead = Size{Pictures: int64(lp), Bytes: int64(lp * sv.LeadBytes), Hours: lp / PerHour}
	all = Size{Pictures: int64(ap), Bytes: int64(lp*sv.LeadBytes + (ap-lp)*sv.OtherBytes), Hours: ap / PerHour}
	return lead, all
}
