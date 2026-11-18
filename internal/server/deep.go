package server

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"runtime"
	"strconv"
	"strings"

	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/fulltext"
	"github.com/vajra-wielder/nabu/internal/library"
	"github.com/vajra-wielder/nabu/internal/wikitext"
)

// mapView places a point on the bundled world map, zoomed in so the
// region is recognisable, entirely offline.
type mapView struct {
	Lat, Lon         float64
	Label            string
	ImgW, ImgL, ImgT float64 // world image size and offset, % of the frame
	DotL, DotT       float64 // dot position, % of the frame
	OSM              string  // online map, for when there is internet
	Nearby           []placeView
}

type placeView struct {
	Title, URL, Dist string
}

func newMapView(lat, lon float64) *mapView {
	const zoom = 3.0
	// Equirectangular: x in [0,1] from -180..180, y from 90..-90. The
	// frame is 2:1 like the map, so both axes use the same percentages.
	x, y := (lon+180)/360, (90-lat)/180
	l := 50 - x*zoom*100
	t := 50 - y*zoom*100
	min := 100 - zoom*100
	l = math.Max(min, math.Min(0, l))
	t = math.Max(min, math.Min(0, t))
	return &mapView{
		Lat: lat, Lon: lon,
		Label: fmtCoord(lat, "N", "S") + " " + fmtCoord(lon, "E", "W"),
		ImgW:  zoom * 100, ImgL: l, ImgT: t,
		DotL: l + x*zoom*100, DotT: t + y*zoom*100,
		OSM: fmt.Sprintf("https://www.openstreetmap.org/?mlat=%.5f&mlon=%.5f#map=9/%.5f/%.5f", lat, lon, lat, lon),
	}
}

func fmtCoord(v float64, pos, neg string) string {
	h := pos
	if v < 0 {
		h, v = neg, -v
	}
	return strconv.FormatFloat(v, 'f', 3, 64) + "°" + h
}

func fmtKm(km float64) string {
	switch {
	case km < 1:
		return strconv.Itoa(int(km*1000+0.5)) + " m"
	case km < 20:
		return strconv.FormatFloat(km, 'f', 1, 64) + " km"
	}
	return strconv.Itoa(int(km+0.5)) + " km"
}

// articleMap builds the location panel of an article.
func (s *Server) articleMap(b *library.Book, title string, out *wikitext.Output) *mapView {
	var c *wikitext.Coord
	for i := range out.Coords {
		if out.Coords[i].Primary {
			c = &out.Coords[i]
			break
		}
	}
	if c == nil && len(out.Coords) > 0 {
		c = &out.Coords[0]
	}
	if c == nil {
		return nil
	}
	m := newMapView(c.Lat, c.Lon)
	if ft := b.FullText(); ft != nil {
		if near, err := ft.Nearby(c.Lat, c.Lon, 8, title); err == nil {
			for _, p := range near {
				m.Nearby = append(m.Nearby, placeView{Title: p.Title, URL: articleURL(b, p.Title), Dist: fmtKm(p.Km)})
			}
		}
	}
	return m
}

// textHit is a full-text search result for the template.
type textHit struct {
	Title, URL string
	Snippet    template.HTML
}

type textSearch struct {
	Status   library.FTStatus
	Results  []textHit
	Total    int
	Ignored  []string
	Page     int
	Pages    int
	Prev     string
	Next     string
	Minutes  int
	Size     int64
	Building bool
}

// searchText runs a search inside articles, with snippets.
func (s *Server) searchText(b *library.Book, q string, page int) *textSearch {
	ts := &textSearch{Status: b.FTStatus(), Page: page}
	ts.Minutes, ts.Size = library.FullTextEstimate(b.Files.Size, runtime.NumCPU()-1)
	ts.Building = ts.Status.State == library.FTBuilding || ts.Status.State == library.FTQueued
	ft := b.FullText()
	if ft == nil {
		return ts
	}
	const per = 20
	res, err := ft.Search(q, (page-1)*per, per)
	if err != nil {
		return ts
	}
	ts.Total, ts.Ignored = res.Total, res.Ignored
	ts.Pages = (res.Total + per - 1) / per
	if ts.Pages > 20 {
		ts.Pages = 20 // results are ranked; beyond 400 they are noise
	}
	rd := b.Reader()
	for _, h := range res.Hits {
		hit := textHit{Title: h.Title, URL: articleURL(b, h.Title)}
		if p, err := rd.Load(dump.Entry{Stream: h.Stream, ID: h.PageID}); err == nil {
			hit.Snippet = template.HTML(fulltext.Snippet(wikitext.PlainText(p.Text, rd.Site()), res.Terms, 220))
		}
		ts.Results = append(ts.Results, hit)
	}
	base := "/" + b.ID + "/search?list=1&q=" + urlQuery(q) + "&p="
	if page > 1 {
		ts.Prev = base + strconv.Itoa(page-1)
	}
	if page < ts.Pages {
		ts.Next = base + strconv.Itoa(page+1)
	}
	return ts
}

func urlQuery(q string) string {
	return strings.ReplaceAll(template.URLQueryEscaper(q), "%20", "+")
}

// servePageData answers GET /nabu/api/page?book=enwiki&title=Babylon with
// the article's structured data: links, categories, templates,
// coordinates, infoboxes, tables, media and glossary.
func (s *Server) servePageData(w http.ResponseWriter, r *http.Request) {
	b := s.Lib.Get(r.URL.Query().Get("book"))
	title := r.URL.Query().Get("title")
	if b == nil || b.Reader() == nil || title == "" {
		http.NotFound(w, r)
		return
	}
	page, err := s.loadFollow(b, title)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d := wikitext.Extract(page.Title, page.Text, b.Site())
	writeJSON(w, map[string]interface{}{
		"wiki": b.Files.DB, "dump": b.Files.Date, "id": page.ID, "edited": page.Timestamp,
		"url": articleURL(b, page.Title), "data": d,
	})
}
