# The held-out workload: transfer results through S019

The held-out workload is a second corpus the teams never saw and that was never scored (METHODS.md, section 8). It
was run at season boundaries, by the operator, to test whether an incumbent's gain against upstream exists outside
the corpora the teams worked on. Its seed, its location and its tooling are not published, and the operator's own
notes are not copied here because they name them. This file restates the numbers of those notes.

Each row is one 12-round job of the incumbent against upstream restic (`ba802d42`) on the held-out workload: one
official judge lane, 4 pinned CPUs, the same seven phases and the same oracle as the visible workloads. All arms
matched the upstream oracle in every job.

| incumbent | date | time saved vs upstream (90% interval; wins) | memory saved | the same tree on visible `main`, time |
|---|---|---|---|---|
| S015 | 2026-10-03 | +23.27% (22.97..23.36; 12/12) | +29.65% (28.55..30.31) | +22.58% |
| S016 with the race fix | 2026-10-03 | +26.59% (26.45..26.76) | +28.58% | +26.2% (a suite canary) |
| S017 | 2026-10-04 | +28.75% (28.65..28.90; 12/12) | +31.26% | +27.61% |
| S018 | 2026-10-05 | +29.91% (29.68..30.08; 12/12) | +36.89% | +29.30% (14 main rounds of the promotion job `821fc91a`) |
| S019 | 2026-10-06 | +30.76% (30.48..30.91; 12/12) | +39.34% (38.85..39.86) | +30.23% (11 main rounds of the promotion job `28102fc2`) |

Upstream's pass took 46.67 s and S019's 32.35 s (S018: 32.68 s against 46.57 s).

By phase, against upstream, time saved % (S019's 90% interval in brackets):

| phase | S015 | S018 | S019 |
|---|---|---|---|
| backup-initial | +9.42 | +11.01 | +12.83 (12.28..13.11) |
| backup-unchanged | +11.62 | +14.84 | +18.11 (18.01..18.37) |
| backup-mutated | +8.57 | +11.94 | +14.38 (14.11..14.63) |
| restore | +37.57 | +49.05 | +47.84 (47.06..48.78) |
| check-read-data | +1.78 | +32.74 | +31.80 (31.55..32.76) |
| forget-prune | +39.62 | +45.30 | +45.40 (45.07..45.76) |
| ls-json | +36.12 | +35.73 | +36.77 (36.54..36.81) |

The note for S019 reads the step from S018 as +0.85 points on the held-out workload against +0.93 points on visible
`main`: "The gain transfers in direction and most of its size." It also records that restore and check read lower
for S019 than for S018 on the held-out workload, inside the noise of two jobs a day apart, as "a thing to watch at
the next boundary, not a finding". There was no next boundary run.

## What this does not cover

- **The tree in this repository has not been run on the held-out workload.** Neither has the S020 incumbent, nor the
  league tree after S019 with its hand merge. S019 is the last tree run on it.
- S019's largest gains were on the visible `large-files` and `remote` workloads, which have no held-out counterpart.
- It is one corpus on one machine at 4 CPUs.
