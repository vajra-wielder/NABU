package pictures

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Wikimedia's rules for programs (wikitech.wikimedia.org/wiki/Robot_policy
// and mediawiki.org/wiki/Wikimedia_APIs/Rate_limits, read November 2026):
//
//   - pictures (upload.wikimedia.org): at most 2 downloads at a time and
//     25 Mbps in total, standard thumbnail sizes only;
//   - API: one request at a time, 200 a minute for a program that names
//     itself in its User-Agent;
//   - on 429 or 503, wait as long as Retry-After says (at least 5 s).
//
// NABU stays below all of these: 2 downloads, 16 Mbps, an API request at
// most every 0.4 s, asking 50 pictures at a time.
const (
	maxDownloads = 2
	maxRate      = 2_000_000 // bytes a second (16 Mbps)
	apiGap       = 400 * time.Millisecond
	apiBatch     = 50
)

var minBackoff = 5 * time.Second

// Errors.
var (
	ErrNotFound = errors.New("pictures: Wikimedia has no such picture")
	ErrBusy     = errors.New("pictures: Wikimedia asked to slow down")
	ErrSkipped  = errors.New("pictures: not a picture NABU keeps (sound or video)")
)

// Info is where a picture is, as the wiki's API says.
type Info struct {
	Name    string // normalised
	Missing bool
	Skip    bool // sound, video
	URL     string
	Mime    string
	Shared  bool // on Wikimedia Commons, so the same for every language
}

// Fetcher downloads from Wikimedia within its rules. One Fetcher is
// shared by everything in NABU, so all of it together stays within them.
type Fetcher struct {
	Client    *http.Client
	UserAgent string

	slots chan struct{}

	mu       sync.Mutex
	apiNext  time.Time // earliest next API request
	rateNext time.Time // when the bytes sent so far are "paid for"
	pause    time.Time // Wikimedia asked to wait until then
	backoff  time.Duration

	apiMu   sync.Mutex // one API request at a time
	batchMu sync.Mutex
	batches map[string]*batch
}

// NewFetcher returns a Fetcher.
func NewFetcher(client *http.Client, userAgent string) *Fetcher {
	return &Fetcher{Client: client, UserAgent: userAgent, slots: make(chan struct{}, maxDownloads), batches: map[string]*batch{}}
}

// Paused reports until when Wikimedia asked NABU to wait.
func (f *Fetcher) Paused() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pause
}

func (f *Fetcher) waitPause(ctx context.Context) error {
	f.mu.Lock()
	d := time.Until(f.pause)
	f.mu.Unlock()
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// slowDown records a 429 or 503: everything waits, at least 5 s, twice
// as long each time it happens again before a request succeeds.
func (f *Fetcher) slowDown(resp *http.Response) {
	d := 0 * time.Second
	if s := resp.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			d = time.Duration(n) * time.Second
		} else if t, err := http.ParseTime(s); err == nil {
			d = time.Until(t)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.backoff < minBackoff {
		f.backoff = minBackoff
	} else if f.backoff < 10*time.Minute {
		f.backoff *= 2
	}
	if d < f.backoff {
		d = f.backoff
	}
	if until := time.Now().Add(d); until.After(f.pause) {
		f.pause = until
	}
}

// succeeded resets the backoff: Wikimedia is answering again.
func (f *Fetcher) succeeded() {
	f.mu.Lock()
	f.backoff = 0
	f.mu.Unlock()
}

func (f *Fetcher) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	return f.Client.Do(req)
}

// Resolve asks the wiki's API where pictures are, 50 at a time.
func (f *Fetcher) Resolve(ctx context.Context, host string, names []string) (map[string]Info, error) {
	out := map[string]Info{}
	for len(names) > 0 {
		n := len(names)
		if n > apiBatch {
			n = apiBatch
		}
		if err := f.resolve(ctx, host, names[:n], out); err != nil {
			return out, err
		}
		names = names[n:]
	}
	return out, nil
}

func (f *Fetcher) resolve(ctx context.Context, host string, names []string, out map[string]Info) error {
	titles := make([]string, len(names))
	for i, n := range names {
		titles[i] = "File:" + Normalize(n)
	}
	q := url.Values{
		"action": {"query"}, "format": {"json"}, "formatversion": {"2"},
		"prop": {"imageinfo"}, "iiprop": {"url|mime"}, "iiurlwidth": {strconv.Itoa(Width)},
		"titles": {strings.Join(titles, "|")}, "redirects": {"1"}, "maxlag": {"5"},
	}
	u := "https://" + host + "/w/api.php?" + q.Encode()
	f.apiMu.Lock()
	defer f.apiMu.Unlock()
	for attempt := 0; ; attempt++ {
		if err := f.waitPause(ctx); err != nil {
			return err
		}
		f.mu.Lock()
		wait := time.Until(f.apiNext)
		f.apiNext = time.Now().Add(apiGap)
		if wait > 0 {
			f.apiNext = f.apiNext.Add(wait)
		}
		f.mu.Unlock()
		if wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		resp, err := f.get(ctx, u)
		if err != nil {
			return err
		}
		if resp.StatusCode == 429 || resp.StatusCode == 503 {
			f.slowDown(resp)
			resp.Body.Close()
			if attempt < 3 {
				continue
			}
			return ErrBusy
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("pictures: %s answered %s", host, resp.Status)
		}
		f.succeeded()
		var r apiResponse
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&r)
		resp.Body.Close()
		if err != nil {
			return err
		}
		if r.Error.Code == "maxlag" {
			f.slowDown(&http.Response{Header: http.Header{"Retry-After": {"5"}}})
			continue
		}
		// Follow the API's renaming (localised "File:", underscores,
		// redirects from old file names) from each name asked for to its
		// page.
		rename := map[string]string{}
		for _, m := range r.Query.Normalized {
			rename[m.From] = m.To
		}
		for _, m := range r.Query.Redirects {
			rename[m.From] = m.To
		}
		byTitle, byName := map[string]int{}, map[string]int{}
		for i, p := range r.Query.Pages {
			byTitle[p.Title] = i
			if j := strings.IndexByte(p.Title, ':'); j >= 0 {
				byName[Normalize(p.Title[j+1:])] = i
			}
		}
		for _, t := range titles {
			final := t
			for i := 0; i < 4; i++ {
				next, ok := rename[final]
				if !ok {
					break
				}
				final = next
			}
			info := Info{Name: strings.TrimPrefix(t, "File:")}
			pi, ok := byTitle[final]
			if !ok {
				if j := strings.IndexByte(final, ':'); j >= 0 {
					pi, ok = byName[Normalize(final[j+1:])]
				}
			}
			if !ok {
				info.Missing = true
				out[info.Name] = info
				continue
			}
			p := r.Query.Pages[pi]
			info.Shared = p.Repository == "shared"
			switch {
			case len(p.ImageInfo) == 0:
				info.Missing = true
			default:
				ii := p.ImageInfo[0]
				info.Mime = ii.Mime
				info.URL = ii.ThumbURL
				if strings.HasPrefix(ii.Mime, "audio/") || strings.HasPrefix(ii.Mime, "video/") || strings.HasPrefix(ii.Mime, "application/ogg") {
					info.Skip = true
				}
				if info.URL == "" {
					info.Skip = true
				}
				if i := strings.IndexByte(info.URL, '?'); i >= 0 {
					info.URL = info.URL[:i] // drop tracking parameters
				}
			}
			out[info.Name] = info
		}
		return nil
	}
}

type apiResponse struct {
	Error struct{ Code string } `json:"error"`
	Query struct {
		Normalized []struct{ From, To string } `json:"normalized"`
		Redirects  []struct{ From, To string } `json:"redirects"`
		Pages      []struct {
			Title      string `json:"title"`
			Repository string `json:"imagerepository"`
			ImageInfo  []struct {
				ThumbURL string `json:"thumburl"`
				Mime     string `json:"mime"`
			} `json:"imageinfo"`
		} `json:"pages"`
	} `json:"query"`
}

// Download fetches a picture. It waits its turn: two at a time, within
// the speed limit, and not while Wikimedia asked to wait.
func (f *Fetcher) Download(ctx context.Context, u string) ([]byte, error) {
	select {
	case f.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-f.slots }()
	if err := f.waitPause(ctx); err != nil {
		return nil, err
	}
	resp, err := f.get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 429 || resp.StatusCode == 503:
		f.slowDown(resp)
		return nil, ErrBusy
	case resp.StatusCode == 404:
		return nil, ErrNotFound
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("pictures: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	f.succeeded()
	if len(data) > maxSize {
		return nil, ErrSkipped
	}
	// Speed limit: each download "costs" its size at maxRate; wait until
	// what was downloaded so far is paid for.
	f.mu.Lock()
	now := time.Now()
	if f.rateNext.Before(now) {
		f.rateNext = now
	}
	f.rateNext = f.rateNext.Add(time.Duration(float64(len(data)) / maxRate * float64(time.Second)))
	wait := time.Until(f.rateNext)
	f.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	return data, nil
}

// batch gathers the pictures one page asks for at once into one API
// request.
type batch struct {
	names []string
	done  chan struct{}
	res   map[string]Info
	err   error
}

// ResolveOne finds one picture, sharing the API request with the other
// pictures asked for in the same moment (those of the same page).
func (f *Fetcher) ResolveOne(ctx context.Context, host, name string) (Info, error) {
	name = Normalize(name)
	f.batchMu.Lock()
	b := f.batches[host]
	if b == nil || len(b.names) >= apiBatch {
		b = &batch{done: make(chan struct{})}
		f.batches[host] = b
		go func() {
			time.Sleep(60 * time.Millisecond)
			f.batchMu.Lock()
			if f.batches[host] == b {
				delete(f.batches, host)
			}
			names := b.names
			f.batchMu.Unlock()
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			b.res, b.err = f.Resolve(cctx, host, names)
			cancel()
			close(b.done)
		}()
	}
	b.names = append(b.names, name)
	f.batchMu.Unlock()
	select {
	case <-ctx.Done():
		return Info{}, ctx.Err()
	case <-b.done:
	}
	if info, ok := b.res[name]; ok {
		return info, nil
	}
	if b.err != nil {
		return Info{}, b.err
	}
	return Info{Name: name, Missing: true}, nil
}
