package fulltext

import "strings"

// Words that carry no meaning of their own (articles, pronouns,
// prepositions, conjunctions, helping verbs) are left out of the index:
// almost every article has them, so they cost much room and never narrow
// a search. Searching for them is still possible by title.
//
// Kept on purpose, because they are also names or abbreviations: English
// "us" (U.S.), "who" (WHO), "will", "may" (the month).
var stopLists = map[string]string{
	"en": `a an the and or but nor if then else so than that this these those there here
		where when what which whom whose why how i me my mine myself we our ours ourselves
		you your yours yourself yourselves he him his himself she her hers herself it its
		itself they them their theirs themselves is am are was were be been being have has
		had having do does did doing done can could would should shall must might of in on
		at by for with from to into onto upon about above below over under between through
		during before after against among within without off out up down as also not no
		very too just only such both each either neither all any some few more most other
		another own same again further once while until because although though whether
		since per via etc`,
	"fr": `le la les un une des de du au aux et ou mais donc or ni car en dans par pour sur
		sous avec sans entre vers chez que qui quoi dont ne pas plus se sa son ses il elle
		ils elles est sont ete etait etaient etre avoir ont avait ce cet cette ces on nous
		vous je tu me te moi toi lui leur leurs mon ma mes ton ta tes notre nos votre vos
		comme aussi tres tout tous toute toutes meme si`,
	"es": `el la los las un una unos unas de del al y e o u ni pero sino en con por para
		sin sobre entre hasta desde hacia que quien quienes cual cuales cuyo se su sus lo
		le les me te nos os mi mis tu tus yo el ella ellos ellas nosotros usted ustedes es
		son fue fueron era eran ser sido estar esta estan como tambien muy mas pero este
		esta estos estas ese esa esos esas aquel aquella`,
	"pt": `os as um uma uns umas de do da dos das em no na nos nas ao aos pelo pela pelos
		pelas por para com sem sobre entre ate desde que quem qual quais se seu sua seus
		suas lhe lhes me te nos eu tu ele ela eles elas voce foi foram era eram ser sido
		estar esta estao como tambem muito mais mas ou nem este esta estes estas esse essa
		esses essas aquele aquela isso isto`,
	"ru": `и в во не что он на я с со как а то все она так его но да ты к у же вы за бы по
		только ее её мне было вот от меня еще ещё нет о из ему когда даже ну ли если уже или ни
		быть был него до вас вам ведь там потом себя ей они тут где есть ней для мы тебя их
		чем была сам чтоб без будто чего тоже себе под будет ж тогда кто этот того потому
		этого какой ним здесь этом мой тем чтобы нее неё были при об после над тот через эти
		нас про всего них какая эту моя свою этой перед им более всегда между также которые
		который которая которое является`,
	"ar": `في من على إلى الى عن مع هذا هذه ذلك تلك التي الذي الذين اللذين هو هي هم هن
		كان كانت كانوا أن ان إن أو او ثم قد لا ما لم لن كل بين حتى عند بعد قبل وقد وفي ومن
		وهو وهي كما أيضا ايضا حيث لكن منذ خلال ضد بها به لها له فيها منها عليه عليها`,
	"fa": `و در به از که این را با است برای آن تا کرد بر هم نیز شد می وی شده بود دارد ها
		های ای اند او ما شود کند یا اما اگر هر بین پس پیش چون همه ولی باید ایشان آنها
		خود`,
	"id": `yang dan di ke dari ini itu dengan untuk pada adalah dalam tidak akan oleh juga
		sebagai atau ada karena ia dia mereka kami kita saya telah sudah bisa dapat lebih
		para serta namun tetapi hingga antara bahwa tersebut merupakan`,
	"da": `og i at det en den til er som pa de med han af for ikke der var mig sig men et
		har om vi min havde ham hun nu over da fra du ud sin dem os op man hans hvor eller
		hvad skal selv her alle vil blev kunne ind nar vaere dog noget ville jo deres efter
		ned skulle denne end dette mit ogsa under have dig hende mine alt meget sit sine vor
		mod disse hvis din nogle hos blive ad bliver hendes vaeret jer`,
}

// Stopwords lists the words of a language left out of the index, folded
// as search folds them. Unknown languages have none (the most common
// words are still dropped by how often they occur).
func Stopwords(lang string) []string {
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	if lang == "simple" {
		lang = "en"
	}
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.Fields(stopLists[lang]) {
		Tokenize(w, func(t string) {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		})
	}
	return out
}

// junk reports terms not worth indexing: long numbers (identifiers,
// coordinates, ISBNs) and long letter-and-digit codes. Years and short
// numbers stay searchable.
func junk(t string) bool {
	digits := 0
	for i := 0; i < len(t); i++ {
		if t[i] >= '0' && t[i] <= '9' {
			digits++
		}
	}
	switch {
	case digits == 0:
		return false
	case digits == len(t):
		return len(t) > 4
	default:
		return len(t) > 8
	}
}
