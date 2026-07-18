# Release pipeline metrics comparison

| Scenario | Jobs (before → after) | Jobs saved | Wall seconds (before → after) | Wall saved | Runner seconds (before → after) | Runner saved |
|---|---:|---:|---:|---:|---:|---:|
| main_ci | 25 → 12 | 13 (52.00%) | 387 → 680 | -293 (-75.71%) | 1253 → 1341 | -88 (-7.02%) |
| pr_ci | 25 → 12 | 13 (52.00%) | 359 → 804 | -445 (-123.96%) | 1205 → 1467 | -262 (-21.74%) |
| publisher | 30 → 7 | 23 (76.67%) | 417 → 119 | 298 (71.46%) | 1280 → 111 | 1169 (91.33%) |
| total | 80 → 31 | 49 (61.25%) | 1163 → 1603 | -440 (-37.83%) | 3738 → 2919 | 819 (21.91%) |

Queue time is not compared because the historical baseline did not record it.
