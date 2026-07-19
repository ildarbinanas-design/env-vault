# Release pipeline metrics comparison

| Scenario | Jobs (before → after) | Jobs saved | Wall seconds (before → after) | Wall saved | Runner seconds (before → after) | Runner saved |
|---|---:|---:|---:|---:|---:|---:|
| main_ci | 25 → 12 | 13 (52.00%) | 387 → 904 | -517 (-133.59%) | 1253 → 1538 | -285 (-22.75%) |
| pr_ci | 25 → 12 | 13 (52.00%) | 359 → 907 | -548 (-152.65%) | 1205 → 1536 | -331 (-27.47%) |
| publisher | 30 → 7 | 23 (76.67%) | 417 → 591 | -174 (-41.73%) | 1280 → 571 | 709 (55.39%) |
| total | 80 → 31 | 49 (61.25%) | 1163 → 2402 | -1239 (-106.53%) | 3738 → 3645 | 93 (2.49%) |

Queue time is not compared because the historical baseline did not record it.
