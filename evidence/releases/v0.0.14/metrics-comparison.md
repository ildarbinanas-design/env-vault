# Release pipeline metrics comparison

| Scenario | Jobs (before → after) | Jobs saved | Wall seconds (before → after) | Wall saved | Runner seconds (before → after) | Runner saved |
|---|---:|---:|---:|---:|---:|---:|
| main_ci | 25 → 12 | 13 (52.00%) | 387 → 270 | 117 (30.23%) | 1253 → 911 | 342 (27.29%) |
| pr_ci | 25 → 12 | 13 (52.00%) | 359 → 289 | 70 (19.50%) | 1205 → 970 | 235 (19.50%) |
| publisher | 30 → 7 | 23 (76.67%) | 417 → 107 | 310 (74.34%) | 1280 → 100 | 1180 (92.19%) |
| total | 80 → 31 | 49 (61.25%) | 1163 → 666 | 497 (42.73%) | 3738 → 1981 | 1757 (47.00%) |

Queue time is not compared because the historical baseline did not record it.
