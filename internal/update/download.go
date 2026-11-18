package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vajra-wielder/nabu/internal/sysutil"
)

// PartSize is the largest piece a download is written in. Anything bigger
// is split into name.zimaa, name.zimab, ... so the result fits on FAT32
// pen drives (4 GiB file limit). Kiwix tools read split files too.
var PartSize int64 = 4_000_000_000

// Job states.
const (
	StateIdle        = "idle"
	StateStarting    = "starting"
	StateChecking    = "checking"    // re-hashing a partial download before resuming
	StateDownloading = "downloading" // receiving bytes
	StateRetrying    = "retrying"    // network hiccup, will resume
	StateFinishing   = "finishing"   // verifying checksum and installing
	StateDone        = "done"
	StateFailed      = "failed"
	StateCancelled   = "cancelled"
)

// Status is a snapshot of the current download for the UI.
type Status struct {
	State    string  `json:"state"`
	File     string  `json:"file,omitempty"`
	Done     int64   `json:"done"`
	Total    int64   `json:"total"`
	Speed    float64 `json:"speed"` // bytes per second
	ETA      int64   `json:"eta"`   // seconds, 0 when unknown
	Error    string  `json:"error,omitempty"`
	Verified bool    `json:"verified,omitempty"`
}

// Manager runs one download at a time into a library directory.
type Manager struct {
	Dir      string
	Client   *http.Client
	OnFinish func(file string) // called after a successful install
	// RetryDelay is the first pause after a network error; it doubles
	// on each further failure, up to two minutes.
	RetryDelay time.Duration

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc
	done   atomic.Int64
	total  atomic.Int64
	speed  atomic.Int64
}

// NewManager prepares downloads into dir.
func NewManager(dir string, client *http.Client) *Manager {
	return &Manager{Dir: dir, Client: client, RetryDelay: 2 * time.Second, status: Status{State: StateIdle}}
}

func (m *Manager) incomingDir(file string) string {
	return filepath.Join(m.Dir, ".nabu", "incoming", strings.TrimSuffix(file, ".zim"))
}

// Status returns the current progress.
func (m *Manager) Status() Status {
	m.mu.Lock()
	s := m.status
	m.mu.Unlock()
	s.Done = m.done.Load()
	if t := m.total.Load(); t > 0 {
		s.Total = t
	}
	s.Speed = float64(m.speed.Load())
	if s.Speed > 0 && s.Total > s.Done && (s.State == StateDownloading) {
		s.ETA = int64(float64(s.Total-s.Done) / s.Speed)
	}
	return s
}

// Busy reports whether a download is in progress.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel != nil
}

// Paused lists partial downloads that can be resumed.
func (m *Manager) Paused() []string {
	ents, _ := os.ReadDir(filepath.Join(m.Dir, ".nabu", "incoming"))
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name()+".zim")
		}
	}
	return out
}

// Discard deletes a partial download.
func (m *Manager) Discard(file string) error {
	if m.Busy() && m.Status().File == file {
		return errors.New("that download is running; cancel it first")
	}
	if !nameRe.MatchString(file) {
		return errors.New("not a Wikipedia edition file name")
	}
	return os.RemoveAll(m.incomingDir(file))
}

// Start begins (or resumes) downloading ed in the background.
func (m *Manager) Start(ed Edition) error {
	if ed.URL == "" || !nameRe.MatchString(ed.File) {
		return errors.New("invalid edition")
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return errors.New("a download is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.status = Status{State: StateStarting, File: ed.File}
	m.done.Store(0)
	m.total.Store(ed.Size)
	m.speed.Store(0)
	m.mu.Unlock()

	go func() {
		err := m.run(ctx, ed)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.cancel = nil
		switch {
		case err == nil:
			m.status.State = StateDone
		case errors.Is(err, context.Canceled):
			m.status.State = StateCancelled
		default:
			m.status.State = StateFailed
			m.status.Error = err.Error()
		}
	}()
	return nil
}

// Cancel stops the running download. The partial file is kept so the
// download can resume later.
func (m *Manager) Cancel() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
}

func (m *Manager) setState(s string) {
	m.mu.Lock()
	m.status.State = s
	m.mu.Unlock()
}

type meta struct {
	URL   string `json:"url"`
	Total int64  `json:"total"`
}

func (m *Manager) run(ctx context.Context, ed Edition) error {
	dir := m.incomingDir(ed.File)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	want, algo := m.fetchChecksum(ctx, ed.URL)

	// Hash whatever we already have so the final checksum covers it.
	var h hash.Hash
	if algo == "sha256" {
		h = sha256.New()
	}
	m.setState(StateChecking)
	have, err := hashExisting(ctx, dir, h, &m.done)
	if err != nil {
		return err
	}

	backoff := m.RetryDelay
	for attempt := 0; ; attempt++ {
		err = m.fetch(ctx, ed, dir, &have, h)
		if err == nil {
			break
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, errFatal) || attempt >= 50 {
			return err
		}
		m.mu.Lock()
		m.status.State = StateRetrying
		m.status.Error = err.Error()
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}

	m.setState(StateFinishing)
	verified := false
	if h != nil && want != "" {
		got := hex.EncodeToString(h.Sum(nil))
		if got != want {
			// Corrupt data: throw it away so a retry starts clean.
			os.RemoveAll(dir)
			return fmt.Errorf("checksum mismatch, the download was corrupted (got %s…, expected %s…); it has been deleted, please try again", got[:12], want[:12])
		}
		verified = true
	}
	if err := m.install(dir, ed.File); err != nil {
		return err
	}
	m.mu.Lock()
	m.status.Verified = verified
	m.status.Error = ""
	m.mu.Unlock()
	if m.OnFinish != nil {
		m.OnFinish(ed.File)
	}
	return nil
}

var errFatal = errors.New("fatal")

type fatalErr struct{ error }

func (f fatalErr) Is(t error) bool { return t == errFatal }
func (f fatalErr) Unwrap() error   { return f.error }

// fetch downloads from offset *have to the end, appending to part files.
func (m *Manager) fetch(ctx context.Context, ed Edition, dir string, have *int64, h hash.Hash) error {
	req, err := http.NewRequestWithContext(ctx, "GET", ed.URL, nil)
	if err != nil {
		return fatalErr{err}
	}
	req.Header.Set("User-Agent", UserAgent)
	if *have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(*have, 10)+"-")
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var total int64
	switch resp.StatusCode {
	case http.StatusPartialContent:
		total = parseContentRangeTotal(resp.Header.Get("Content-Range"))
	case http.StatusOK:
		if *have > 0 {
			// Server ignored the range: start over.
			if err := clearParts(dir); err != nil {
				return fatalErr{err}
			}
			*have = 0
			m.done.Store(0)
			if h != nil {
				h.Reset()
			}
		}
		total = resp.ContentLength
	case http.StatusRequestedRangeNotSatisfiable:
		if t := readMeta(dir).Total; t > 0 && *have == t {
			return nil // already complete
		}
		clearParts(dir)
		*have = 0
		m.done.Store(0)
		if h != nil {
			h.Reset()
		}
		return errors.New("server rejected resume point; restarting")
	case http.StatusNotFound, http.StatusGone:
		return fatalErr{fmt.Errorf("%s is no longer offered (%s); refresh the catalogue", ed.File, resp.Status)}
	default:
		return fmt.Errorf("server answered %s", resp.Status)
	}
	if total > 0 {
		m.total.Store(total)
		writeMeta(dir, meta{URL: ed.URL, Total: total})
		if need := total - *have; need > 0 {
			if free, err := sysutil.FreeSpace(m.Dir); err == nil && free < need+64<<20 {
				return fatalErr{fmt.Errorf("not enough space on the drive: need %s more, only %s free",
					HumanBytes(need), HumanBytes(free))}
			}
		}
	}
	m.setState(StateDownloading)

	buf := make([]byte, 1<<20)
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	lastTick, lastBytes := time.Now(), *have
	for {
		n, rerr := resp.Body.Read(buf)
		data := buf[:n]
		for len(data) > 0 {
			idx := *have / PartSize
			room := PartSize - *have%PartSize
			chunk := data
			if int64(len(chunk)) > room {
				chunk = chunk[:room]
			}
			if f == nil || *have%PartSize == 0 {
				if f != nil {
					f.Close()
				}
				f, err = os.OpenFile(partName(dir, idx), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				if err != nil {
					return fatalErr{err}
				}
			}
			if _, err := f.Write(chunk); err != nil {
				return fatalErr{friendlyWriteErr(err)}
			}
			if h != nil {
				h.Write(chunk)
			}
			*have += int64(len(chunk))
			m.done.Store(*have)
			data = data[len(chunk):]
		}
		if now := time.Now(); now.Sub(lastTick) >= time.Second {
			rate := float64(*have-lastBytes) / now.Sub(lastTick).Seconds()
			old := float64(m.speed.Load())
			if old > 0 {
				rate = 0.7*old + 0.3*rate
			}
			m.speed.Store(int64(rate))
			lastTick, lastBytes = now, *have
		}
		if rerr == io.EOF {
			if total > 0 && *have < total {
				return io.ErrUnexpectedEOF
			}
			if f != nil {
				if err := f.Sync(); err != nil {
					return fatalErr{friendlyWriteErr(err)}
				}
			}
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

func friendlyWriteErr(err error) error {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no space"):
		return errors.New("the drive is full")
	case strings.Contains(msg, "too large"):
		return errors.New("the drive's file system cannot hold files this large; reformat it as exFAT")
	case strings.Contains(msg, "read-only"):
		return errors.New("the drive is read-only (check its lock switch)")
	}
	return err
}

func partName(dir string, idx int64) string {
	return filepath.Join(dir, fmt.Sprintf("part-%c%c", 'a'+byte(idx/26), 'a'+byte(idx%26)))
}

func listParts(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "part-[a-z][a-z]"))
	sort.Strings(matches)
	return matches, err
}

func clearParts(dir string) error {
	parts, _ := listParts(dir)
	for _, p := range parts {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

// hashExisting feeds the parts already on disk into h and returns their
// combined length. A partial last part is fine; a short middle part is
// not, so everything after it is dropped.
func hashExisting(ctx context.Context, dir string, h hash.Hash, progress *atomic.Int64) (int64, error) {
	parts, err := listParts(dir)
	if err != nil {
		return 0, err
	}
	var have int64
	buf := make([]byte, 1<<20)
	for i, p := range parts {
		if p != partName(dir, int64(i)) {
			for _, q := range parts[i:] {
				os.Remove(q)
			}
			break
		}
		f, err := os.Open(p)
		if err != nil {
			return 0, err
		}
		st, _ := f.Stat()
		size := st.Size()
		if size > PartSize {
			f.Close()
			return 0, fmt.Errorf("%s is larger than expected; discard the download and retry", p)
		}
		if h != nil {
			r := bufio.NewReaderSize(f, 1<<20)
			for {
				if ctx.Err() != nil {
					f.Close()
					return 0, ctx.Err()
				}
				n, err := r.Read(buf)
				h.Write(buf[:n])
				progress.Add(int64(n))
				if err == io.EOF {
					break
				}
				if err != nil {
					f.Close()
					return 0, err
				}
			}
		} else {
			progress.Add(size)
		}
		f.Close()
		have += size
		if size < PartSize {
			for _, q := range parts[i+1:] {
				os.Remove(q)
			}
			break
		}
	}
	return have, nil
}

// install moves finished parts into the library under their final names.
func (m *Manager) install(dir, file string) error {
	parts, err := listParts(dir)
	if err != nil || len(parts) == 0 {
		return errors.New("download finished but no data was found")
	}
	base := strings.TrimSuffix(file, ".zim")
	var targets []string
	if len(parts) == 1 {
		targets = []string{filepath.Join(m.Dir, file)}
	} else {
		for i := range parts {
			targets = append(targets, filepath.Join(m.Dir, fmt.Sprintf("%s.zim%c%c", base, 'a'+byte(i/26), 'a'+byte(i%26))))
		}
	}
	for i, p := range parts {
		if err := os.Rename(p, targets[i]); err != nil {
			return fmt.Errorf("installing %s: %w", filepath.Base(targets[i]), err)
		}
	}
	return os.RemoveAll(dir)
}

func (m *Manager) fetchChecksum(ctx context.Context, fileURL string) (sum, algo string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", fileURL+".sha256", nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := m.Client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if s := sha256Re.FindString(strings.ToLower(string(b))); s != "" {
		return s, "sha256"
	}
	return "", ""
}

var sha256Re = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

func parseContentRangeTotal(v string) int64 {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v[i+1:]), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func readMeta(dir string) meta {
	var mt meta
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err == nil {
		json.Unmarshal(b, &mt)
	}
	return mt
}

func writeMeta(dir string, mt meta) {
	b, _ := json.Marshal(mt)
	os.WriteFile(filepath.Join(dir, "meta.json"), b, 0o644)
}

// HumanBytes formats a byte count for people: 1.4 GB, 980 MB. It counts
// in 1024s, like Windows and the Kiwix download listing, so the sizes
// people see in NABU match the ones they see there.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGTPE"[exp])
}
