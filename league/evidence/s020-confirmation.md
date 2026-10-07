# The confirmation of the S020 incumbent

The S020 incumbent is the league's last promoted tree and the `incumbent` arm of the final jobs. It is not the tree
in this repository (it carries three lock-wait mechanisms that the review dropped, and it stores some blobs in a form
upstream does not). Its confirmation, from the season review
([`../reviews/seasons/S020-review.md`](../reviews/seasons/S020-review.md)): job `7398d456ee4e4911ba83654ded3ad77f`, 44
rounds, 4 pinned CPUs, 2026-10-07 02:35 to 04:17 UTC, all arms matched the upstream oracle (1,019 of 1,019 units).
The job's result file is not included here; the review states that the table was checked against the machine's
records.

Cells show wins of 11 and the 90% interval.

| against | suite | main | big-index | large-files | remote | wins |
|---|---|---|---|---|---|---|
| league tree after S019, time | +1.03% (+0.89..+1.14) | +0.33 (8/11; -0.29..+0.67) | +3.46 (11/11; +3.14..+3.51) | +0.24 (10/11; +0.15..+0.43) | +0.11 (6/11; -0.17..+0.41) | 35/44 |
| league tree after S019, memory | +0.51 | +1.31 | -0.46 | +0.49 | +0.71 | |
| upstream, time | +29.94% (+29.79..+30.01) | +31.28 (11/11; +31.12..+31.49) | +45.55 (11/11; +45.34..+45.70) | +23.97 (11/11; +23.64..+24.28) | +18.95 (11/11; +18.64..+19.05) | 44/44 |
| upstream, memory | +43.91 | +43.18 | +46.34 | +39.45 | +46.67 | |

Median seconds (S020 / league tree after S019 / upstream): suite 76.51 / 77.20 / 109.18; main 15.25 / 15.32 / 22.20;
big-index 17.48 / 18.08 / 32.06; large-files 12.35 / 12.38 / 16.21; remote 31.44 / 31.42 / 38.72.

The same tree is measured again, as the incumbent arm, in every result file under `judge/` whose reference is S020.
