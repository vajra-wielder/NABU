package server

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vajra-wielder/nabu/internal/collect"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/pictures"
	"github.com/vajra-wielder/nabu/internal/web"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

// savedPageTmpl is a collection's copy of an article: one HTML file with
// its styles built in, readable in any browser with or without NABU.
var savedPageTmpl = template.Must(template.New("saved").Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}" dir="{{.Dir}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="NABU">
<title>{{.Title}} · {{.Collection}}</title>
<style>{{.CSS}}
.saved-head{background:var(--bar);color:var(--bar-ink);border-bottom:3px solid var(--clay);padding:10px 20px;font:15px/1.3 var(--sans)}
.saved-head a{color:var(--bar-ink)}.saved-head b{font-family:var(--serif);letter-spacing:.14em}
.reader{display:block}.page{max-width:var(--read-width);margin:0 auto}</style>
<script src="collection.js"></script>
<script>document.addEventListener('error',function(e){var t=e.target;if(t&&t.tagName==='IMG'&&t.parentNode&&t.parentNode.className==='pic')t.parentNode.className='pic broken';},true);</script>
</head>
<body class="wiki">
<div class="saved-head" dir="ltr"><b>NABU</b> · <a href="index.html">{{.Collection}}</a></div>
<div class="reader"><main class="page">
<h1 class="firstHeading" dir="auto">{{.Title}}</h1>
<div class="tagline" dir="ltr"><span>Saved copy from {{.Wiki}}, dump of {{.Dump}}{{if .Edited}}, article last edited {{.Edited}}{{end}}. Saved {{.Saved}}.</span> <a href="{{.Original}}" rel="noopener">Original article ↗</a></div>
<article class="mw-body">
{{.Body}}
</article>
<footer class="page-foot" dir="ltr"><p>Text from Wikipedia, “<a href="{{.Original}}">{{.Title}}</a>”, by its contributors, under the <a href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a> licence. Saved with NABU.</p></footer>
</main></div>
<script>(function(){
var m=window.NABU_COLLECTION||{},a=document.querySelectorAll('a[data-t]');
for(var i=0;i<a.length;i++){var f=m[a[i].getAttribute('data-t')];if(f){var h=a[i].getAttribute('href'),k=h.indexOf('#');a[i].setAttribute('href',encodeURIComponent(f)+(k>=0?h.slice(k):''));}}
var im=document.getElementsByTagName('img');for(var j=0;j<im.length;j++)if(im[j].complete&&!im[j].naturalWidth&&im[j].parentNode.className==='pic')im[j].parentNode.className='pic broken';
})();</script>
</body>
</html>
`))

var linkRe = regexp.MustCompile(`<a href="/([a-z0-9_]+(?:-\d{8})?)/wiki/([^"]*)" title="([^"]*)">`)

// savedCopy renders page into a self-contained HTML document. Links to
// other articles point at the original wiki online and carry a data-t
// tag, through which the collection's own copies take over.
func (s *Server) savedCopy(b *library.Book, page *dump.Page, collection string) ([]byte, collect.Item, map[string][]byte, error) {
	site := b.Site()
	host := site.Host()
	db := b.Files.DB
	out := wikitext.Render(page.Text, &wikitext.Context{
		Title:    page.Title,
		Site:     site,
		PageURL:  func(t string) string { return articleURL(b, t) },
		ImageURL: func(file string, width int) string { return imageURL(db, file) },
	})
	// Pictures go into the copy's own folder, from the drive or (online)
	// from Wikimedia, so the copy shows them offline anywhere. One that
	// cannot be had now points at Wikimedia, for when it is online.
	files := map[string][]byte{}
	picNames := map[string]string{}
	st := s.settings()
	online := st.Pictures && !s.Offline
	out.HTML = picSrcRe.ReplaceAllStringFunc(out.HTML, func(m string) string {
		name, err := url.PathUnescape(picSrcRe.FindStringSubmatch(m)[1])
		if err != nil {
			return m
		}
		if f, ok := picNames[name]; ok {
			return `<img src="` + f + `"`
		}
		f := `https://` + host + `/wiki/Special:FilePath/` + wikiPath(name) + `?width=` + itoa(int64(pictures.Width))
		if ps := s.Lib.Pictures(); ps != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			data, err := ps.Get(ctx, db, host, name, online, st.KeepPictures)
			cancel()
			if err == nil {
				fn := strconv.Itoa(len(files)+1) + picExt(data)
				files[fn] = data
				f = collect.FilesToken + fn
			}
		}
		picNames[name] = f
		return `<img src="` + f + `"`
	})
	body := linkRe.ReplaceAllStringFunc(out.HTML, func(m string) string {
		p := linkRe.FindStringSubmatch(m)
		if p[1] != b.ID {
			return m
		}
		return `<a href="https://` + host + `/wiki/` + p[2] + `" data-t="` + b.ID + `|` + p[3] + `" title="` + p[3] + `">`
	})
	var css bytes.Buffer
	for _, name := range []string{"theme.css", "wiki.css"} {
		c, err := fs.ReadFile(web.Assets, "assets/"+name)
		if err != nil {
			return nil, collect.Item{}, nil, err
		}
		css.Write(c)
		css.WriteByte('\n')
	}
	it := collect.Item{Book: b.ID, Wiki: wikiName(b.Files.DB), Host: host, Title: page.Title,
		Dump: b.Files.Date, Edited: page.Timestamp}
	var buf bytes.Buffer
	err := savedPageTmpl.Execute(&buf, map[string]interface{}{
		"Lang": site.Lang, "Dir": dirOf(site), "Title": page.Title, "Collection": collection,
		"CSS": template.CSS(css.String()), "Wiki": it.Wiki, "Dump": dump.HumanDate(b.Files.Date),
		"Edited": strings.SplitN(page.Timestamp, "T", 2)[0], "Saved": time.Now().Format("2 January 2006"),
		"Original": "https://" + host + "/wiki/" + wikiPath(page.Title), "Body": template.HTML(body),
	})
	return buf.Bytes(), it, files, err
}

var picSrcRe = regexp.MustCompile(`<img src="/nabu/img/[^/"]+/([^"?]+)\?w=\d+"`)

// picExt is the file extension for a picture's contents.
func picExt(b []byte) string {
	switch http.DetectContentType(b) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	return ".jpg"
}

// collectInto saves an article into a collection.
func (s *Server) collectInto(slug, bookID, title string) (*collect.Collection, error) {
	c, err := s.collections.Get(slug)
	if err != nil {
		return nil, errors.New("no such collection")
	}
	b := s.Lib.Get(bookID)
	if b == nil || b.Reader() == nil {
		return nil, errors.New("that Wikipedia is not available")
	}
	page, err := s.loadFollow(b, title)
	if err != nil {
		return nil, errors.New("no such article")
	}
	html, it, files, err := s.savedCopy(b, page, c.Name)
	if err != nil {
		return nil, err
	}
	return s.collections.Add(slug, it, html, []byte(page.Text), files)
}

type collectionRow struct {
	Slug  string `json:"slug"`
	Name  string `json:"name"`
	Count int    `json:"count"`
	Has   bool   `json:"has"`
}

// serveCollectionsAPI answers GET /nabu/api/collections?book=&title=:
// every collection, and whether it holds that article.
func (s *Server) serveCollectionsAPI(w http.ResponseWriter, r *http.Request) {
	book, title := r.URL.Query().Get("book"), r.URL.Query().Get("title")
	rows := []collectionRow{}
	for _, c := range s.collections.List() {
		rows = append(rows, collectionRow{Slug: c.Slug, Name: c.Name, Count: len(c.Items), Has: c.Has(book, title)})
	}
	writeJSON(w, rows)
}

// collectionAction handles the POST actions on collections.
func (s *Server) collectionAction(name string, body map[string]string) (interface{}, error) {
	switch name {
	case "collection-create":
		c, err := s.collections.Create(body["name"])
		if err != nil {
			return nil, err
		}
		if body["book"] != "" && body["title"] != "" {
			if c, err = s.collectInto(c.Slug, body["book"], body["title"]); err != nil {
				return nil, err
			}
		}
		return map[string]string{"slug": c.Slug}, nil
	case "collection-add":
		_, err := s.collectInto(body["slug"], body["book"], body["title"])
		return nil, err
	case "collection-remove":
		return nil, s.collections.Remove(body["slug"], body["book"], body["title"])
	case "collection-rename":
		return nil, s.collections.Rename(body["slug"], body["name"])
	case "collection-delete":
		return nil, s.collections.Delete(body["slug"])
	case "collection-refresh":
		c, err := s.collections.Get(body["slug"])
		if err != nil {
			return nil, err
		}
		refreshed, skipped := 0, 0
		for _, it := range c.Items {
			if b := s.Lib.Get(it.Book); b != nil && b.Reader() != nil {
				if _, err := s.collectInto(c.Slug, it.Book, it.Title); err == nil {
					refreshed++
					continue
				}
			}
			skipped++
		}
		return map[string]int{"refreshed": refreshed, "skipped": skipped}, nil
	}
	return nil, errNoAction
}

var errNoAction = errors.New("unknown action")

// serveCollections handles /nabu/collections, a collection's page, its
// files, and the whole collection as one printable book.
func (s *Server) serveCollections(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/nabu/collections"), "/")
	if rest == "" {
		http.Redirect(w, r, "/nabu/saved#collections", http.StatusFound)
		return
	}
	slug, file, _ := strings.Cut(rest, "/")
	c, err := s.collections.Get(slug)
	if err != nil {
		s.errorPage(w, r, http.StatusNotFound, "No such collection", "It may have been deleted.")
		return
	}
	switch file {
	case "":
		s.render(w, r, "collection.html", map[string]interface{}{
			"PageTitle": c.Name, "Nav": "saved", "C": c, "Items": s.collectionItems(c),
			"Folder": filepath.Join(s.collections.Dir, c.Slug),
		})
		return
	case "book":
		s.serveCollectionBook(w, r, c)
		return
	}
	p, err := s.collections.File(slug, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(file, ".html"):
		// Inside NABU, links to the wiki go to the local copy instead.
		page := string(b)
		for _, it := range c.Items {
			if bk := s.Lib.Get(it.Book); bk != nil && it.Host != "" {
				page = strings.ReplaceAll(page, `href="https://`+it.Host+`/wiki/`, `href="/`+bk.ID+`/wiki/`)
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
	case strings.HasSuffix(file, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(b)
	case strings.HasSuffix(file, ".wiki"):
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(b)
	case strings.Contains(file, "_files/"):
		w.Header().Set("Content-Type", http.DetectContentType(b))
		w.Write(b)
	default:
		http.NotFound(w, r)
	}
}

type collectionItemView struct {
	collect.Item
	SavedURL, LiveURL, DumpDate, Newer string
}

func (s *Server) collectionItems(c *collect.Collection) []collectionItemView {
	var out []collectionItemView
	for _, it := range c.Items {
		v := collectionItemView{Item: it, SavedURL: "/nabu/collections/" + url.PathEscape(c.Slug) + "/" + url.PathEscape(it.File),
			DumpDate: dump.HumanDate(it.Dump)}
		if b := s.Lib.Get(it.Book); b != nil && b.Reader() != nil {
			v.LiveURL = articleURL(b, it.Title)
			if b.Files.Date > it.Dump {
				v.Newer = dump.HumanDate(b.Files.Date)
			}
		}
		out = append(out, v)
	}
	return out
}

var articleBodyRe = regexp.MustCompile(`(?s)<article class="mw-body">(.*)</article>`)

// serveCollectionBook shows every saved page of a collection one after
// another, with contents, ready to print or save as PDF.
func (s *Server) serveCollectionBook(w http.ResponseWriter, r *http.Request, c *collect.Collection) {
	type chapter struct {
		Anchor, Title string
		Body          template.HTML
	}
	var chapters []chapter
	anchors := map[string]string{}
	for i, it := range c.Items {
		anchors[it.Book+"|"+it.Title] = "ch" + itoa(int64(i+1))
	}
	dataT := regexp.MustCompile(`href="[^"]*" data-t="([^"]*)"`)
	for i, it := range c.Items {
		p, err := s.collections.File(c.Slug, it.File)
		if err != nil {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		m := articleBodyRe.FindSubmatch(b)
		if m == nil {
			continue
		}
		body := dataT.ReplaceAllStringFunc(string(m[1]), func(x string) string {
			k := dataT.FindStringSubmatch(x)[1]
			if a, ok := anchors[htmlUnescape(k)]; ok {
				return `href="#` + a + `" data-t="` + k + `"`
			}
			return x
		})
		chapters = append(chapters, chapter{Anchor: "ch" + itoa(int64(i+1)), Title: it.Title, Body: template.HTML(body)})
	}
	s.render(w, r, "collection-book.html", map[string]interface{}{"PageTitle": c.Name, "C": c, "Chapters": chapters})
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&#39;", "'", "&quot;", `"`, "&lt;", "<", "&gt;", ">").Replace(s)
}
