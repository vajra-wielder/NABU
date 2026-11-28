# Tests to run offline (on a flight)

Everything here works with no internet. Times are measured on Simple English on a
4-core machine, and estimated for English (about 60 times the size); a slow laptop or
a USB 2 stick can be two to four times slower. Tick each one and note anything odd
(what you did, what you saw, the time it took): that is all I need to fix it.

**Before boarding**, with internet: double-click **Set up NABU** for your computer on
the drive and let it finish (see the end of this page). Then pull the computer's
internet and start NABU once to make sure it opens.

| # | Test | Takes | What you should see |
|---|---|---|---|
| 1 | **Start.** Double-click **Start NABU**. | 5 s (first time on a computer: +2 s to copy the program to it) | The browser opens on your library. The bottom says *Safe to pull out the drive at any time*. |
| 2 | **Read.** Open 10 articles from links, search and Random. | 2 min | Each opens in well under a second (4–7 ms of work; the rest is the browser). Pictures kept on the drive show; the others show a striped box with the picture's name. |
| 3 | **Search titles.** Type in the top box: a few letters, a misspelling, no accents ("ecole"). | 1 min | Suggestions while typing, under a millisecond each. |
| 4 | **Search inside articles.** Search a phrase that is not a title ("boil water for one minute"). Then "the" alone, "us army", "US army". | 2 min | Results with the words marked. "the": told it is too common. "US army" finds articles with the abbreviation; "us army" searches only "army". |
| 5 | **Pull the drive out.** With an article open, pull the drive out without ejecting it. Scroll the open page, then click a link. | 1 min | The open page keeps working. The link shows *The NABU drive is not connected* and retries every 3 s. |
| 6 | **Plug it back in**, into another USB port if you have one. | 30 s | Within about 3 seconds the page you asked for opens by itself. On Windows the drive may get another letter: NABU finds it anyway. Windows may offer to scan the drive; let it. |
| 7 | **Pull it out while it writes.** On the home page, start something that writes (Build search inside articles on a small language, or Keep main pictures); while the footer says *Writing to the drive*, pull it out; plug it back in. | 3 min | The job carries on from where it was (or restarts cleanly), nothing else lost. Then run test 9. |
| 8 | **Essentials.** Saved & recent → open *Essentials: First aid and emergencies* → open 3 articles. Then, in the file manager, open `collections/Essentials__First_aid_and_emergencies/index.html` on the drive directly (NABU not needed). | 3 min | Each article opens as a page of its own, with its pictures if setup was online. |
| 9 | **Check the drive** (bottom of any page). | English 19 min (measured, Windows laptop), Simple English 20 s | A live timer (so far · left), then *all … bundles are intact*. If anything is damaged, the articles are named. |
| 10 | **Read on phones.** Turn on your phone's hotspot (if it allows it in flight mode; it needs no signal or data). Join it from the laptop. On *Read on phones*, press **Share on Wi-Fi**; scan the QR code with the phone. | 3 min | NABU opens on the phone. Try reading and searching there. Press **Stop sharing**: the phone can no longer load pages. |
| 11 | **Look.** Night theme (**Aa** → Night), a phone-sized window, a right-to-left page if you have Arabic, Persian or Yiddish. | 3 min | Nothing runs off the side of the screen; text readable in both themes. |
| 12 | **Make a copy** onto a second stick, if you carry one: English, your computer's programs, no pictures. | English ~15–30 min (USB 3), longer on USB 2 | A progress bar and a working NABU on the second stick. Start it from there to check. A spare also lets *Check the drive* repair the first one. |
| 13 | **Turn off NABU** (bottom of any page). | 5 s | *NABU has stopped*; the window says it is safe to remove the drive. |

## Setting up, once online (before or after the flight)

Double-click **Set up NABU (Windows / Mac)** or run `sh set-up-nabu-linux.sh`. It does
everything in order, each step with a live timer, and prints the time each took at the
end (also kept in `.work/setup-log.txt` on the drive):

| Step | English, on a 4-core laptop | Simple English (measured) |
|---|---|---|
| 1. Check the connection | seconds | seconds |
| 2. Download to the computer, write to the drive without the pages NABU never shows | download: ~1 h at 50 Mbit/s, ~6 h at 10 Mbit/s; writing: ~2 h | download depends on the line; writing 1 min |
| 3. Title index | ~15 min | 1 s |
| 4. Search inside articles | 2 h 24 min before the build was made faster; now about 30–45 min (timed before it starts) | 24 s |
| 5. Essentials with pictures | ~5–15 min (about 2,000 pictures, fetched in batches) | finding the pictures 3 s; saving the articles 5 s |
| 6. Main pictures | started; ~1 week of NABU running online, longest articles first | — |

It needs about 30 GB free on the computer for the download (otherwise it downloads to
the drive). Stop it whenever you like (Ctrl+C or close the window) and run it again: it
carries on, and next month it updates. When it finishes it starts NABU, so the pictures
keep coming in while NABU runs.

## The automatic test (realtest)

From the NABU checkout, with the dumps in the test folder: a folder named like
**NABU-test** beside the checkout is found by itself (or set `NABU_TEST` to another one):

- Windows, in Command Prompt, PowerShell or Windows Terminal: `scripts\realtest.bat`
  (needs Git for Windows; runs in that same window).
- Mac or Linux: `scripts/realtest.sh`

Each step shows its progress live; the report is in `realtest/report.txt` inside the test folder.
Give another folder as the first argument, `WIKI=simplewiki` for a smaller wiki,
or `CRAWL=300` for a shorter run.
