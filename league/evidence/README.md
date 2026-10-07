# Evidence

The files the published numbers come from. All judge measurements: one machine, 4 pinned CPUs, the `performance`
frequency governor.

| path | what it is |
|---|---|
| `source-identity.json` | written when this repository was assembled: which commit of the league's working repository the source equals (every blob id and path, and SHA-256 of every file of the last source commit), the ids of the commits here, which commit the official jobs measured, and which files changed between that commit and the published source |
| `judge/index.json` | one entry per judge result: job id, mode, rounds, start and end time, candidate and incumbent labels, the SHA-256 of the judge's original result file, and how often the path rule below changed it |
| `judge/roles.json` | which result is the headline, which are listed as arms, and which commit was judged; read by `league/tools/make_tables.py` |
| `judge/*.result.json` | the judge's result files, schema `restic-linux-judge-v1` |
| `machine.txt` | the machine every judge job, bench and release check of the final tree ran on: CPU, caches and their domains, memory, kernel, and which CPUs the judge and the release checks used. It is the file behind "128 GB" and "48 cores" in the documents |
| `upstream-identity/UPSTREAM-IDENTITY.txt` | the comparison of commit 1 of this repository with upstream restic at `ba802d42`, made on 2026-10-07 through GitHub's API: all eleven top-level entries equal. It was made before every commit here was given the publishing account's identity; that changed the commit id of commit 1 and none of the tree and file ids the record compares |
| `judged-vs-published/PROOF.txt`, `diff.offsets`, `proof.sh` | the comparison of the binary the judge measured with a build of the published source, section by section; the list of the 60 byte offsets that differ; the script that made both (its paths are the league box's) |
| `independent-rebuild/offline-proof.json`, `offline-targets.json` | records of an independent reviewer's offline rebuilds: the judged and the published commit with the judge's flags (both hashes, and the same 60 differing offsets), and four of the five release binaries with rebuilt and published hash side by side. The reviewer reports the fifth, linux/amd64, as rebuilt to the published hash as well; that report is not in this repository |
| `SHA256SUMS` | the SHA-256 checksums of the five release binaries built from the published commit; the files were checked against this list when the repository was assembled. They are not the judged binary: `league/docs/RESULTS.md`, section 2 |
| `many-core.json` | every table of the merge agent's record of the bench at 8 and 16 cores and of the many-directory restore, parsed from it; a copy of that record is `league/reviews/merge/MANY-CORE.md` |
| `many-core-rc9/` | the files behind the current tables of `many-core.json`: the per-pair results of the 8- and 16-core bench on the published machine code (`mc8-rc9-*.json`, `mc16-rc9-*.json`, each with a status file), the table made from them (`mc-rc9-table.txt`), the scripts (`mc2.sh`, `mcbench.py`, `mcsum.py`), and the cache-layout test of the many-directory restore (`l3dirs.sh`, `l3dirs.log`, `l3dirs-progress-lines.txt`). `SHA256SUMS.original` is that folder's own checksum list |
| `release-checks-final3-b8cf439/` | the release-check harness's own records for the judged commit `b8cf439`: `status.txt` (one line per finished check or diagnostic) and `table.md`; `c10/summary.json` (the scale check); `x11-606060/` (the many-directory restore beside the judge job), `x11-606060-performance/` and `x11-606060-schedutil-quiet/` (the governor test), `x11-large-partial/` (the larger sizes, a stopped run), each with `runs.json` holding every run; `cpufreq/` (the saved governor state, the record of setting and restoring it, the clock samples); `published-commit-8f9372b/status.txt` (what ran on the published commit) |
| `checks/final-check-table.md` | every result line of those status files, quoted, for reading beside the tables of `league/docs/SOUNDNESS.md`, section 8 |
| `carried-files.json` | the index of every file in the six folders and the one file above that were copied from the league's records: SHA-256 of the original, SHA-256 of the published copy, and how often the path rule changed it |
| `s020-confirmation.md` | the confirmation of the league's last incumbent, as its season review records it |
| `holdout/README.md` | the held-out workload's transfer results through S019 |

## The path rule

One rule was applied to every published copy, here and under `league/tools/` and `league/reviews/`: **the prefix of
the league box's working directory reads `/opt/league/`.** Nothing else about a path was changed. So
`/opt/league/restic-league-v2/...` and `/opt/league/restic-final/...` in these files are the league box's layout
with that one substitution. A file in which the prefix did not occur is byte-identical to its original.
`carried-files.json` and `judge/index.json` keep the SHA-256 of every original, so a copy can be matched with the
file it was made from. `many-core-rc9/SHA256SUMS.original` lists the hashes of that folder's originals: the scripts
in it, which the rule changed, do not verify against it, and the result files do.

## The judge results

| name | job | what was measured |
|---|---|---|
| `repaired-12r` | `3c21cf80` | the repaired tree against the league tree after S019: the price of the repairs as first staged; 12 rounds |
| `rawrule-rc3-12r` | `aca189ed` | the final merge before fix F2 (incompressible blobs still stored raw) against the S020 incumbent; 12 rounds |
| `final-rc5-12r` | `757b745c` | the first final tree against S020; 12 rounds |
| `final-rc5-44r` | `c87fa304` | the first final tree against S020; 44 rounds, no other judge job or bench beside it |
| `final2-rc6-12r` | `f6765696` | the second final tree one commit before its last (restore's tree-ID bound at 65,536) against S020; 12 rounds |
| `final2-rc7-12r` | `d5c8c004` | the second final tree, the one the last independent review was given, against S020; 12 rounds |
| `final2-rc7-44r` | `940c4e64` | the second final tree against S020; 44 rounds, no other judge job or bench beside it |
| `final3-rc8-12r` | `9fc9eafd` | the judged commit of this release against S020; 12 rounds |
| `final3-rc8-44r` | `50388f4e` | **the judged commit of this release against S020; 44 rounds: the headline** |

Every job has three arms: `candidate`, `incumbent` and `stock` (upstream restic at `ba802d42`), so each file also
holds the candidate against upstream. The names `rc-3` to `rc-8` are the league's labels for exports of its working
repository. `rc-8` is the judged commit; the published source is one commit later and differs from it in one comment
and one test (`source-identity.json`; `league/docs/RESULTS.md`, section 2).

**The copies differ from the judge's files in three ways only:** the key `local_evidence`, a path on the
workstation that fetched the result, is removed; the JSON is written with LF line ends; and the path rule above is
applied (nine paths per file). `index.json` holds the SHA-256 of each original file. Paths that start with `/opt/`
inside the results are the league box's layout.

Two more job ids of the final merge measured nothing and their files are not included: `5d43382d` (failed at start)
and `ef51c559` (stopped in its warm-up).

## What is not included

- The result files of the twenty seasons' confirmations and of the hand merges. Their numbers are quoted in
  `league/docs/RESULTS.md` from the season reviews and merge reports, which give the job ids; the reviews state that
  the confirmation tables were checked against the machine's records.
- The per-pair files of the **earlier** many-core bench, on the second final tree, and the run logs of the
  many-directory restores on the bench lanes (the before-and-after table). `many-core.json` holds their tables; the
  merge agent's record names the files on the league box.
- The per-run records and logs of the bench figures quoted from the merge reports (the cost of each repair and
  fix), and of the release checks other than the files listed above: the checks' own evidence folders stayed on the
  league box. The check agent's report and step log are under `league/reviews/checks/`.
- The clock samples of the governor test are included; the report's summary of them (mean clock, share of readings
  at the minimum) was made by a tool on the league box (`league/tools/checks/tools/x11_clock_table.py`) and was not
  recomputed when this repository was assembled.
- The held-out workload, its generator and its result files; the league's engine; the role transcripts.
- The independent reviewers' full reports on this write-up; two of their record files are carried
  (`independent-rebuild/`).

So the final judge results, the judge and workload sources, the release-check harness, its status files and the
review reports are published. Historical confirmations and the earlier many-core results are documented summaries.
`league/tools/league/launch.py` is configuration evidence, not a league engine that can be run.

## Reading a result file

`comparisons.candidate_vs_stock` and `comparisons.candidate_vs_incumbent` hold `time_saved_pct`, `rss_saved_pct`,
`runtime_wins`, `rounds`, `interval90`, and the same per workload and per phase. `arms.<arm>.per_workload.<w>.per_phase.<p>`
holds every round's seconds and peak memory. `correctness` holds the oracle's verdict; `measurement` says how time and
memory were taken; `eligibility` is the league's own promotion verdict. `python league/tools/make_tables.py`
regenerates every published table from these files, including the 90% intervals of the per-phase cells, which it
computes from the per-round values with the judge's own function.
