package update

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vajra-wielder/nabu/internal/drive"
	"github.com/vajra-wielder/nabu/internal/dump"
	"github.com/vajra-wielder/nabu/internal/sysutil"
	"github.com/vajra-wielder/nabu/internal/timer"
)

// PartSize is the largest piece a file is written in. Bigger files are
// saved as name.bz2aa, name.bz2ab, ... so they fit on FAT32 drives (4 GiB
// file limit). NABU reads the pieces as one file.
var PartSize int64 = 4_000_000_000

// Job states.
const (
	StateIdle        = "idle"
	StateStarting    = "starting"
	StateChecking    = "checking"    // re-hashing a partial download before resuming
	StateDownloading = "downloading" // receiving bytes
	StateRetrying    = "retrying"    // network hiccup, will resume
	StateFinishing   = "finishing"   // installing
	StateSlimming    = "slimming"    // leaving out the pages NABU never shows
	StateDone        = "done"
	StateFailed      = "failed"
	StateCancelled   = "cancelled"
)

// Status is a snapshot of the current download.
type Status struct {
	State    string  `json:"state"`
	Release  string  `json:"release,omitempty"` // e.g. enwiki-20261101
	File     string  `json:"file,omitempty"`    // file being fetched
	Done     int64   `json:"done"`              // bytes, all files
	Total    int64   `json:"total"`
	Speed    float64 `json:"speed"`
	ETA      int64   `json:"eta"`
	Error    string  `json:"error,omitempty"`
	Verified bool    `json:"verified,omitempty"`
	Timer    string  `json:"timer,omitempty"` // time so far and left, while writing to the drive

	phaseAt time.Time
}

// Manager runs one download at a time into a library directory.
//
// A download goes to the computer's own disk when it has room (Computer),
// so the drive needs no room kept free for the next edition: only the
// finished dump is written to the drive, already without the pages NABU
// never shows (Slim). Otherwise it goes to the drive (Work).
type Manager struct {
	Dir        string // finished dumps go here
	Work       string // downloads in progress, on the drive
	Computer   string // downloads in progress, on this computer ("" to never use it)
	Slim       bool   // leave out the pages NABU never shows (dump.Slim)
	Client     *http.Client
	OnFinish   func(rel *Release)
	RetryDelay time.Duration
	// MakeRoom is asked for room on the drive before a finished dump is
	// written there; it may remove older editions of the same wiki.
	MakeRoom func(wiki string, need int64) error

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc
	done   atomic.Int64
	speed  atomic.Int64
}

// NewManager prepares downloads into the drive's wikipedia folder.
func NewManager(d *drive.Layout, client *http.Client) *Manager {
	return &Manager{Dir: d.Wikipedia, Work: d.Incoming(), Computer: ComputerIncoming(), Slim: true,
		Client: client, RetryDelay: 2 * time.Second, status: Status{State: StateIdle}}
}

// ComputerIncoming is where downloads go on this computer: its cache
// folder (kept across restarts, so a download resumes), or "" if none.
func ComputerIncoming() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "NABU", "incoming")
}

func (m *Manager) places() []string {
	if m.Computer != "" && m.Computer != m.Work {
		return []string{m.Computer, m.Work}
	}
	return []string{m.Work}
}

// incoming is where a release is (being) downloaded: where it was
// started, or a new folder on the computer when it has room for it.
func (m *Manager) incoming(key string) string {
	for _, p := range m.places() {
		if _, err := os.Stat(filepath.Join(p, key, "release.json")); err == nil {
			return filepath.Join(p, key)
		}
	}
	return filepath.Join(m.Work, key)
}

func (m *Manager) incomingFor(rel *Release) string {
	dir := m.incoming(rel.Key())
	if _, err := os.Stat(filepath.Join(dir, "release.json")); err == nil {
		return dir
	}
	if m.Computer != "" {
		os.MkdirAll(m.Computer, 0o755)
		if free, err := sysutil.FreeSpace(m.Computer); err == nil && free > rel.Size()+1<<30 {
			return filepath.Join(m.Computer, rel.Key())
		}
	}
	return dir
}

func (m *Manager) onDrive(dir string) bool {
	return filepath.Dir(dir) == filepath.Clean(m.Work)
}

// Status returns the current progress.
func (m *Manager) Status() Status {
	m.mu.Lock()
	s := m.status
	m.mu.Unlock()
	s.Done = m.done.Load()
	s.Speed = float64(m.speed.Load())
	if s.State == StateDownloading && s.Speed > 0 && s.Total > s.Done {
		s.ETA = int64(float64(s.Total-s.Done) / s.Speed)
	}
	if s.State == StateSlimming && !s.phaseAt.IsZero() {
		el := time.Since(s.phaseAt)
		s.ETA = int64(timer.Remaining(el, s.Done, s.Total).Seconds())
		s.Timer = timer.Line(el, s.Done, s.Total)
	}
	return s
}

// Busy reports whether a download is running.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancel != nil
}

// Paused lists partial downloads, with the release saved for resuming.
func (m *Manager) Paused() []Release {
	var out []Release
	for _, place := range m.places() {
		ents, _ := os.ReadDir(place)
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			var rel Release
			b, err := os.ReadFile(filepath.Join(place, e.Name(), "release.json"))
			if err == nil && json.Unmarshal(b, &rel) == nil && rel.Wiki != "" {
				out = append(out, rel)
			}
		}
	}
	return out
}

// Discard deletes a partial download.
func (m *Manager) Discard(key string) error {
	if strings.ContainsAny(key, `/\.`) || key == "" {
		return errors.New("bad name")
	}
	if m.Busy() && m.Status().Release == key {
		return errors.New("that download is running; pause it first")
	}
	return os.RemoveAll(m.incoming(key))
}

// Start begins (or resumes) downloading rel in the background.
func (m *Manager) Start(rel *Release) error {
	if rel == nil || len(rel.Files) == 0 || !wikiRe.MatchString(rel.Wiki) {
		return errors.New("invalid release")
	}
	for _, f := range rel.Files {
		if strings.ContainsAny(f.Name, `/\`) || !strings.HasPrefix(f.Name, rel.Wiki+"-") {
			return errors.New("invalid file name in release")
		}
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return errors.New("a download is already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.status = Status{State: StateStarting, Release: rel.Key(), Total: rel.Size()}
	m.done.Store(0)
	m.speed.Store(0)
	m.mu.Unlock()

	go func() {
		err := m.run(ctx, rel)
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

// Cancel pauses the running download; what was received is kept.
func (m *Manager) Cancel() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
}

func (m *Manager) set(f func(s *Status)) {
	m.mu.Lock()
	f(&m.status)
	m.mu.Unlock()
}

func (m *Manager) run(ctx context.Context, rel *Release) error {
	dir := m.incomingFor(rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rj, _ := json.Marshal(rel)
	os.WriteFile(filepath.Join(dir, "release.json"), rj, 0o644)

	// Room for what is still missing: on the drive (+ the index NABU
	// builds afterwards, ~2% of the dump), or on this computer.
	var have int64
	for _, f := range rel.Files {
		have += partsSize(filepath.Join(dir, f.Name))
	}
	if m.onDrive(dir) {
		if free, err := sysutil.FreeSpace(m.Dir); err == nil {
			need := rel.Size() - have + rel.Size()/50 + 64<<20
			if free < need {
				return fmt.Errorf("not enough space on the drive: %s needed, %s free", HumanBytes(need), HumanBytes(free))
			}
		}
	} else if free, err := sysutil.FreeSpace(dir); err == nil && free < rel.Size()-have {
		return fmt.Errorf("not enough space on this computer: %s needed, %s free", HumanBytes(rel.Size()-have), HumanBytes(free))
	}

	var before int64 // bytes of earlier files, for overall progress
	for _, f := range rel.Files {
		f := f
		m.set(func(s *Status) { s.File = f.Name })
		if err := m.fetchFile(ctx, f, filepath.Join(dir, f.Name), before); err != nil {
			return err
		}
		before += f.Size
	}
	m.set(func(s *Status) { s.State = StateFinishing })
	if err := m.finish(ctx, rel, dir); err != nil {
		return err
	}
	os.RemoveAll(dir)
	m.set(func(s *Status) { s.Verified = true; s.Error = "" })
	if m.OnFinish != nil {
		m.OnFinish(rel)
	}
	return nil
}

// fetchFile downloads one file (resuming), then checks its SHA-1.
func (m *Manager) fetchFile(ctx context.Context, f RemoteFile, base string, before int64) error {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(base, "ok")); err == nil && string(b) == f.SHA1 {
		m.done.Store(before + f.Size)
		return nil // finished in an earlier session
	}
	h := sha1.New()
	m.set(func(s *Status) { s.State = StateChecking })
	have, err := hashExisting(ctx, base, h, func(n int64) { m.done.Store(before + n) })
	if err != nil {
		return err
	}
	if have > f.Size {
		clearParts(base)
		have = 0
		h.Reset()
	}
	backoff := m.RetryDelay
	for attempt := 0; have < f.Size; attempt++ {
		err := m.fetch(ctx, f, base, &have, h, before)
		if err == nil {
			break
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, errFatal) || attempt >= 60 {
			return err
		}
		m.set(func(s *Status) { s.State = StateRetrying; s.Error = err.Error() })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}
	if f.SHA1 != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA1 {
			clearParts(base)
			return fmt.Errorf("%s arrived damaged (checksum mismatch) and was deleted; please try again", f.Name)
		}
	}
	os.WriteFile(filepath.Join(base, "ok"), []byte(f.SHA1), 0o644)
	return nil
}

var errFatal = errors.New("fatal")

type fatalErr struct{ error }

func (f fatalErr) Is(t error) bool { return t == errFatal }
func (f fatalErr) Unwrap() error   { return f.error }

func (m *Manager) fetch(ctx context.Context, f RemoteFile, base string, have *int64, h hash.Hash, before int64) error {
	req, err := http.NewRequestWithContext(ctx, "GET", f.URL, nil)
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
	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		if *have > 0 { // the server ignored the range: start again
			if err := clearParts(base); err != nil {
				return fatalErr{err}
			}
			*have = 0
			h.Reset()
		}
	case http.StatusNotFound, http.StatusGone:
		return fatalErr{fmt.Errorf("%s is no longer on the server; check for a newer dump", f.Name)}
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return fmt.Errorf("the server is busy (%s)", resp.Status)
	default:
		return fmt.Errorf("server answered %s", resp.Status)
	}
	m.set(func(s *Status) { s.State = StateDownloading; s.Error = "" })

	buf := make([]byte, 1<<20)
	var out *os.File
	defer func() {
		if out != nil {
			out.Close()
		}
	}()
	tick, tickBytes := time.Now(), *have
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
			if out == nil || *have%PartSize == 0 {
				if out != nil {
					out.Close()
				}
				out, err = os.OpenFile(partName(base, idx), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
				if err != nil {
					return fatalErr{err}
				}
			}
			if _, err := out.Write(chunk); err != nil {
				return fatalErr{friendlyWriteErr(err)}
			}
			h.Write(chunk)
			*have += int64(len(chunk))
			m.done.Store(before + *have)
			data = data[len(chunk):]
		}
		if now := time.Now(); now.Sub(tick) >= time.Second {
			rate := float64(*have-tickBytes) / now.Sub(tick).Seconds()
			if old := float64(m.speed.Load()); old > 0 {
				rate = 0.7*old + 0.3*rate
			}
			m.speed.Store(int64(rate))
			tick, tickBytes = now, *have
		}
		if rerr == io.EOF {
			if *have < f.Size {
				return io.ErrUnexpectedEOF
			}
			if out != nil {
				if err := out.Sync(); err != nil {
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
		return errors.New("the drive's file system cannot hold files this large")
	case strings.Contains(msg, "read-only"):
		return errors.New("the drive is read-only (check its lock switch)")
	}
	return err
}

func partName(base string, idx int64) string {
	return filepath.Join(base, fmt.Sprintf("part-%c%c", 'a'+byte(idx/26), 'a'+byte(idx%26)))
}

func listParts(base string) []string {
	m, _ := filepath.Glob(filepath.Join(base, "part-[a-z][a-z]"))
	sort.Strings(m)
	return m
}

func partsSize(base string) int64 {
	var n int64
	for _, p := range listParts(base) {
		if st, err := os.Stat(p); err == nil {
			n += st.Size()
		}
	}
	return n
}

func clearParts(base string) error {
	for _, p := range listParts(base) {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

// hashExisting feeds the parts already on disk into h. A short part can
// only be the last one; anything after it is dropped.
func hashExisting(ctx context.Context, base string, h hash.Hash, progress func(int64)) (int64, error) {
	var have int64
	buf := make([]byte, 1<<20)
	parts := listParts(base)
	for i, p := range parts {
		if p != partName(base, int64(i)) {
			for _, q := range parts[i:] {
				os.Remove(q)
			}
			break
		}
		f, err := os.Open(p)
		if err != nil {
			return 0, err
		}
		r := bufio.NewReaderSize(f, 1<<20)
		var size int64
		for {
			if ctx.Err() != nil {
				f.Close()
				return 0, ctx.Err()
			}
			n, err := r.Read(buf)
			h.Write(buf[:n])
			size += int64(n)
			progress(have + size)
			if err == io.EOF {
				break
			}
			if err != nil {
				f.Close()
				return 0, err
			}
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

// finish puts a downloaded release on the drive: without the pages NABU
// never shows when it can, else as it came.
func (m *Manager) finish(ctx context.Context, rel *Release, dir string) error {
	if m.Slim {
		names := map[string][]string{}
		for _, f := range rel.Files {
			names[f.Name] = listParts(filepath.Join(dir, f.Name))
		}
		if f, err := dump.FromNamed(names); err == nil {
			// A slim dump is about 80-85% of the original; English's
			// project pages alone are 18%.
			if err := m.room(rel.Wiki, rel.Size()*9/10+64<<20); err != nil {
				return err
			}
			m.set(func(s *Status) { s.State, s.Total, s.phaseAt = StateSlimming, f.Size, time.Now() })
			m.done.Store(0)
			_, err := dump.Slim(f, m.Dir, dump.SlimOptions{
				PartSize: PartSize,
				Stop:     ctx.Done(),
				Name:     func(p string) string { return filepath.Base(filepath.Dir(p)) }, // dir/<name>/part-aa
				Progress: func(done, total int64) { m.done.Store(done) },
			}, nil)
			switch {
			case err == nil:
				return nil
			case ctx.Err() != nil:
				return ctx.Err()
			}
			// Keep what was downloaded: install it as it came.
			m.set(func(s *Status) {
				s.State, s.Error = StateFinishing, "could not leave out the pages NABU never shows: "+err.Error()
			})
		}
	}
	if !m.onDrive(dir) {
		if err := m.room(rel.Wiki, rel.Size()+64<<20); err != nil {
			return err
		}
	}
	for _, f := range rel.Files {
		if err := m.install(filepath.Join(dir, f.Name), f.Name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) room(wiki string, need int64) error {
	if m.MakeRoom != nil {
		return m.MakeRoom(wiki, need)
	}
	if free, err := sysutil.FreeSpace(m.Dir); err == nil && free < need {
		return fmt.Errorf("not enough space on the drive: %s needed, %s free", HumanBytes(need), HumanBytes(free))
	}
	return nil
}

// install moves (or, from this computer, copies) a finished file into
// the library under its real name (pieces get the suffixes aa, ab, ...).
func (m *Manager) install(base, name string) error {
	parts := listParts(base)
	if len(parts) == 0 {
		return fmt.Errorf("%s: no data", name)
	}
	for i, p := range parts {
		target := filepath.Join(m.Dir, name)
		if len(parts) > 1 {
			target += fmt.Sprintf("%c%c", 'a'+byte(i/26), 'a'+byte(i%26))
		}
		if err := os.Rename(p, target); err != nil {
			if err := CopyFile(p, target); err != nil { // another disk
				return fmt.Errorf("installing %s: %w", filepath.Base(target), friendlyWriteErr(err))
			}
		}
	}
	return nil
}

// CopyFile copies src to dst through a temporary name, flushed to the
// drive before it takes its real name.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".copying"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, bufio.NewReaderSize(in, 1<<20))
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// HumanBytes formats a size in 1024s, as Windows and most file managers do.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	if v >= 100 {
		return fmt.Sprintf("%.0f %cB", v, "KMGTPE"[exp])
	}
	return fmt.Sprintf("%.1f %cB", v, "KMGTPE"[exp])
}
