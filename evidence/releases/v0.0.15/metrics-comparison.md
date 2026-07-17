# Release pipeline metrics comparison

| Scenario | Jobs (before → after) | Jobs saved | Wall seconds (before → after) | Wall saved | Runner seconds (before → after) | Runner saved |
|---|---:|---:|---:|---:|---:|---:|
| main_ci | 25 → 12 | 13 (52.00%) | 387 → 240 | 147 (37.98%) | 1253 → 910 | 343 (27.37%) |
| pr_ci | 25 → 12 | 13 (52.00%) | 359 → 270 | 89 (24.79%) | 1205 → 893 | 312 (25.89%) |
| publisher | 30 → 7 | 23 (76.67%) | 417 → 546 | -129 (-30.94%) | 1280 → 527 | 753 (58.83%) |
| total | 80 → 31 | 49 (61.25%) | 1163 → 1056 | 107 (9.20%) | 3738 → 2330 | 1408 (37.67%) |

Queue time is not compared because the historical baseline did not record it.
