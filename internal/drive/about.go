package drive

import (
	"os"
	"path/filepath"
)

// AboutFile is the note in each folder saying what it holds.
const AboutFile = "ABOUT THIS FOLDER.txt"

// Part describes one folder of the drive.
type Part struct {
	Folder   string // relative to the NABU folder, with forward slashes
	Name     string
	Needed   bool // NABU does not work without it
	About    string
	LeaveOut string // what is lost without it
}

// Parts lists the drive's folders.
var Parts = []Part{
	{Folder: "programs/windows", Name: "Programs for Windows", About: "NABU for Windows 7 to 11, on Intel/AMD (32- and 64-bit) and ARM.",
		LeaveOut: "the drive no longer starts on Windows."},
	{Folder: "programs/mac", Name: "Programs for Mac", About: "NABU for macOS 10.13 and newer, on Intel and Apple silicon.",
		LeaveOut: "the drive no longer starts on a Mac."},
	{Folder: "programs/linux", Name: "Programs for Linux", About: "NABU for Linux and Chromebooks (Linux), on Intel/AMD and ARM (Raspberry Pi).",
		LeaveOut: "the drive no longer starts on Linux."},
	{Folder: Wikipedia, Name: "Wikipedia", Needed: true, About: "The Wikipedias themselves: two files per language, exactly as Wikimedia publishes them " +
		"(…-pages-articles-multistream.xml.bz2 and …-index.txt.bz2). To leave a language out, leave out its two files.",
		LeaveOut: "nothing to read."},
	{Folder: Indexes, Name: "Title indexes", About: "NABU's sorted list of the titles of each Wikipedia, about 2-3% of the dump's size.",
		LeaveOut: "nothing: NABU rebuilds them the first time it starts (minutes for small languages, up to about 15 for English)."},
	{Folder: Search, Name: "Search inside articles", About: "An index of every word of every article, about 12% of the dump's size, built on request. Little words (\"the\", \"can\", \"they\") are left out.",
		LeaveOut: "search finds titles only; it can be built again from the search page."},
	{Folder: Pictures, Name: "Pictures", About: "Wikipedia's pictures, kept for reading offline: one folder per language.",
		LeaveOut: "pictures show only when the computer is online; offline, a labelled frame shows where each one goes."},
	{Folder: Collections, Name: "Collections", About: "Your collections. Each one is a folder of standalone article copies; open its index.html in any browser, even without NABU.",
		LeaveOut: "the collections."},
	{Folder: Personal, Name: "Personal", About: "Your saved articles, reading history and settings.",
		LeaveOut: "nothing important: leave it out when giving NABU to someone else, so your reading stays private."},
	{Folder: Source, Name: "Source code", About: "NABU's source code and the exact tools to build it again (see README.md inside the zip).",
		LeaveOut: "nothing needed to read."},
	{Folder: Work, Name: "Work in progress", About: "Downloads in progress and temporary files.",
		LeaveOut: "nothing: unfinished downloads start again."},
}

// Note is the text of a folder's ABOUT file.
func (p Part) Note() string {
	s := p.Name + "\r\n\r\n" + p.About + "\r\n\r\n"
	if p.Needed {
		s += "NABU needs this folder.\r\n"
	} else {
		s += "This folder can be left out of a copy of NABU to save space. Without it: " + p.LeaveOut + "\r\n"
	}
	return s
}

// WriteNotes puts an ABOUT file in each of the drive's folders that
// exists (all of them with create), so whoever copies the drive knows
// what each one is.
func (l *Layout) WriteNotes(create bool) {
	for _, p := range Parts {
		dir := filepath.Join(l.Root, filepath.FromSlash(p.Folder))
		if p.Folder == Work {
			continue
		}
		if _, err := os.Stat(dir); err != nil {
			if !create || p.Folder == Source {
				continue
			}
			os.MkdirAll(dir, 0o755)
		}
		f := filepath.Join(dir, AboutFile)
		if b, err := os.ReadFile(f); err == nil && string(b) == p.Note() {
			continue
		}
		os.WriteFile(f, []byte(p.Note()), 0o644)
	}
	if _, err := os.Stat(filepath.Join(l.Root, Programs)); err == nil {
		os.WriteFile(filepath.Join(l.Root, Programs, AboutFile), []byte("Programs\r\n\r\nNABU itself, one folder per kind of computer. "+
			"Keep the folders for the computers the drive will be used on; leave the others out to save space.\r\n"), 0o644)
	}
}
