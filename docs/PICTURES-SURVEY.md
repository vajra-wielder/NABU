# How much room pictures take

Measured in November 2026 with `tools/picture-survey` on the newest dump of each of the
ten Wikipedias. The README's sizes and NABU's own estimates (`internal/pictures/estimate.go`)
come from these numbers.

## Method

1. Random stretches of each dump were read (HTTP range requests, starting at the next
   bzip2 stream), and every article in them rendered exactly as NABU renders it. The
   pictures NABU would show were recorded: the first one is the article's *main
   picture*.
2. Up to 300 picture names per wiki were looked up with the wiki's API, to see which are
   the wiki's own files and which come from Wikimedia Commons.
3. For the Commons ones, the English Wikipedia API (`prop=fileusage`, articles only)
   said whether an English article uses the same file. Those pictures are kept once, in
   `pictures/commons`, and are not downloaded again for the other language.
4. About fifteen main pictures and fifteen others per wiki were downloaded at 250 px.
   That is too few to tell the wikis apart, so the sizes are pooled: a main picture is
   24.8 KB on average and another picture 31.0 KB.

Flags, icons and logos (4–7% of pictures shown) are used by many articles and so take
almost no room; they are counted once.

## Results

| Wikipedia | Articles sampled | With a picture | Pictures per article | Own files | Commons pictures English also uses |
|---|---:|---:|---:|---:|---:|
| English | 2,818 | 45% | 0.96 | 8% | — |
| French | 4,225 | 44% | 1.27 | 5% | 48% |
| Russian | 3,300 | 25% | 1.01 | 5% | 48% |
| Spanish | 4,135 | 41% | 1.00 | 0% | 60% (50 checked) |
| Chinese | 4,741 | 39% | 1.12 | 4% | 67% |
| Portuguese | 5,031 | 51% | 1.07 | 8% | not measured; 61% assumed |
| Arabic | 6,940 | 20% | 0.52 | 3% | 69% |
| Persian | 7,783 | 57% | 1.01 | 17% | 72% |
| Indonesian | 5,657 | 49% | 1.23 | 11% | 64% |
| Danish | 7,211 | 47% | 1.15 | 0% | 56% (100 checked) |

Sizes per wiki (on its own, then once English's pictures are kept):

| Wikipedia | Articles | Main pictures | All pictures | Main, after English | All, after English |
|---|---:|---:|---:|---:|---:|
| English | 7,266,259 | 3.3 M · 82 GB | 6.5 M · 181 GB | — | — |
| French | 2,790,246 | 1.2 M · 30 GB | 3.3 M · 95 GB | 16 GB | 51 GB |
| Russian | 2,130,134 | 0.5 M · 14 GB | 2.0 M · 60 GB | 7 GB | 33 GB |
| Spanish | 2,150,721 | 0.9 M · 22 GB | 2.0 M · 57 GB | 9 GB | 23 GB |
| Chinese | 1,566,794 | 0.6 M · 15 GB | 1.6 M · 45 GB | 5 GB | 16 GB |
| Portuguese | ~1,170,000 | 0.6 M · 15 GB | 1.2 M · 33 GB | 7 GB | 14 GB |
| Arabic | 1,340,029 | 0.3 M · 7 GB | 0.7 M · 19 GB | 2 GB | 6 GB |
| Persian | ~1,050,000 | 0.6 M · 15 GB | 1.0 M · 26 GB | 6 GB | 10 GB |
| Indonesian | 805,141 | 0.4 M · 10 GB | 0.9 M · 26 GB | 4 GB | 11 GB |
| Danish | 316,786 | 0.1 M · 4 GB | 0.3 M · 10 GB | 2 GB | 4 GB |
| **All ten** | | 212 GB | 551 GB | **140 GB** | **350 GB** |

## How sure

- Article samples are a few thousand per wiki, so the shares are within a few percent.
- Picture sizes rest on about 300 downloads in all: ±20% on the totals.
- The shared totals only take out pictures English also has. Pictures shared by two other
  languages (French and Spanish, say) are still counted twice, so 140 GB and 350 GB are
  somewhat high.
- Two article counts (Portuguese, Persian) could not be read from the dump and are
  rounded published figures.

## Run it again

```bash
go run ./tools/picture-survey -json survey.json
```

It reads a few hundred MB from dumps.wikimedia.org and makes a few thousand API
requests, within Wikimedia's limits for programs; expect an hour or two.
