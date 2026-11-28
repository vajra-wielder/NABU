package dump

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/vajra-wielder/nabu/internal/testfolder"
)

// fastPage reads pages as encoding/xml does.
func checkPages(t *testing.T, data []byte) (n, fast int) {
	t.Helper()
	for {
		i := bytes.Index(data, pageOpen)
		if i < 0 {
			return
		}
		data = data[i:]
		j := bytes.Index(data, pageClose)
		if j < 0 {
			return
		}
		chunk := data[:j+len(pageClose)]
		data = data[j+len(pageClose):]
		want, err := xmlPageOf(chunk)
		got, ok := fastPage(chunk)
		n++
		if !ok {
			continue // left to encoding/xml
		}
		fast++
		if err != nil {
			t.Fatalf("fast read a page encoding/xml refuses: %s", chunk[:80])
		}
		if *got != *want {
			t.Fatalf("page %d differs:\n got  %+v\n want %+v", want.ID, *got, *want)
		}
	}
}

func TestFastPageSmall(t *testing.T) {
	data := []byte(`<page><title>A &amp; B</title><ns>0</ns><id>7</id><redirect title="C &quot;D&quot;" /><revision><id>9</id><timestamp>2020-01-01T00:00:00Z</timestamp><text bytes="3" xml:space="preserve">x &lt;ref&gt; &#233;&#x4e2d;</text></revision></page>
<page><title>E</title><ns>14</ns><id>8</id><revision><timestamp>t</timestamp><text bytes="0" /></revision></page>
<page><title>F</title><ns>0</ns><id>10</id><revision><text><![CDATA[x]]></text></revision></page>`)
	if n, fast := checkPages(t, data); n != 3 || fast != 2 {
		t.Fatalf("%d pages, %d read fast", n, fast)
	}
}

// On real dumps (NABU_REAL_DUMPS): every page of a sample of bundles.
func TestFastPageReal(t *testing.T) {
	dir := os.Getenv("NABU_REAL_DUMPS")
	if dir == "" && testfolder.Asked("FastPageReal") {
		if f := testfolder.ForTests(); f != "" {
			dir = filepath.Join(f, "wikipedia")
		}
	}
	if dir == "" {
		t.Skip("run with -run FastPageReal (uses the NABU-test folder), or set NABU_REAL_DUMPS")
	}
	eds, _ := Scan(dir)
	for _, f := range eds {
		ix, err := OpenIndex(filepath.Join(filepath.Dir(dir), "indexes", f.Key()+".nidx"), f)
		if err != nil {
			t.Skip(err)
		}
		rd, err := NewReader(f, ix)
		if err != nil {
			t.Fatal(err)
		}
		total, all, fast := ix.StreamCount(), 0, 0
		for i := 0; i < 500; i++ {
			pair, start, end, err := ix.streamRange(uint32(i * total / 500))
			if err != nil {
				t.Fatal(err)
			}
			data, err := rd.stream(pair, start, end)
			if err != nil {
				t.Fatal(err)
			}
			n, k := checkPages(t, data)
			all += n
			fast += k
		}
		t.Logf("%s: %d pages the same, %d of them read fast", f.Key(), all, fast)
		rd.Close()
	}
}
