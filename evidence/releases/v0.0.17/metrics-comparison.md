# Release pipeline metrics comparison

| Scenario | Jobs (before → after) | Jobs saved | Wall seconds (before → after) | Wall saved | Runner seconds (before → after) | Runner saved |
|---|---:|---:|---:|---:|---:|---:|
| main_ci | 25 → 12 | 13 (52.00%) | 387 → 902 | -515 (-133.07%) | 1253 → 1619 | -366 (-29.21%) |
| pr_ci | 25 → 12 | 13 (52.00%) | 359 → 797 | -438 (-122.01%) | 1205 → 1437 | -232 (-19.25%) |
| publisher | 30 → 7 | 23 (76.67%) | 417 → 537 | -120 (-28.78%) | 1280 → 520 | 760 (59.38%) |
| total | 80 → 31 | 49 (61.25%) | 1163 → 2236 | -1073 (-92.26%) | 3738 → 3576 | 162 (4.33%) |

Queue time is not compared because the historical baseline did not record it.
