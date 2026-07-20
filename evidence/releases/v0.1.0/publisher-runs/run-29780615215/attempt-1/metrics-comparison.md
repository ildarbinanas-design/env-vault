# Release pipeline metrics comparison

| Scenario | Jobs (before → after) | Jobs saved | Wall seconds (before → after) | Wall saved | Runner seconds (before → after) | Runner saved |
|---|---:|---:|---:|---:|---:|---:|
| main_ci | 25 → 12 | 13 (52.00%) | 387 → 928 | -541 (-139.79%) | 1253 → 1620 | -367 (-29.29%) |
| pr_ci | 25 → 12 | 13 (52.00%) | 359 → 941 | -582 (-162.12%) | 1205 → 1604 | -399 (-33.11%) |
| publisher | 30 → 7 | 23 (76.67%) | 417 → 624 | -207 (-49.64%) | 1280 → 605 | 675 (52.73%) |
| total | 80 → 31 | 49 (61.25%) | 1163 → 2493 | -1330 (-114.36%) | 3738 → 3829 | -91 (-2.43%) |

Queue time is not compared because the historical baseline did not record it.
