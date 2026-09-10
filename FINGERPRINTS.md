# Release fingerprints

Every prebuilt database is validated with a 20-metric **fingerprint** — row
counts and money totals spanning the OLTP, web, and warehouse layers — before it
is published. After you restore a download (or build from source), run the
fingerprint query for your engine and confirm the row matches your tier in the
tables below.

A match proves the database is **byte-faithful**, not merely that the file
arrived intact. (A plain SHA-256 tells you the *download* is good; the
fingerprint tells you the *restore* is good.)

Because the generator is deterministic, these are also the numbers a
**from-source** build of the same tier produces — the download and the build are
the same database.

## The query

Columns, in order:

```
tx  lines  pays  movs  tradeins  custs  tx_total  line_total  qty
reviews  rating_sum  page_views  pr_runs  pl_runid_sum  pl_net
msum_rev  msum_wages  dw_fact  jan1_fb  fraud_n
```

<details>
<summary><strong>SQL Server</strong></summary>

```sql
SELECT
 (SELECT COUNT_BIG(*) FROM dbo.transactions)                                          tx,
 (SELECT COUNT_BIG(*) FROM dbo.transaction_lines)                                     lines,
 (SELECT COUNT_BIG(*) FROM dbo.payments)                                              pays,
 (SELECT COUNT_BIG(*) FROM dbo.inventory_movements)                                   movs,
 (SELECT COUNT_BIG(*) FROM dbo.trade_ins)                                             tradeins,
 (SELECT COUNT_BIG(*) FROM dbo.customers)                                             custs,
 (SELECT SUM(CAST(total      AS DECIMAL(38,2))) FROM dbo.transactions)                tx_total,
 (SELECT SUM(CAST(line_total AS DECIMAL(38,2))) FROM dbo.transaction_lines)           line_total,
 (SELECT SUM(CAST(quantity   AS BIGINT))        FROM dbo.transaction_lines)           qty,
 (SELECT COUNT_BIG(*) FROM web.reviews)                                               reviews,
 (SELECT SUM(CAST(rating AS BIGINT)) FROM web.reviews)                                rating_sum,
 (SELECT COUNT_BIG(*) FROM web.page_views)                                            page_views,
 (SELECT COUNT_BIG(*) FROM hr.payroll_runs)                                           pr_runs,
 (SELECT SUM(CAST(payroll_run_id AS BIGINT)) FROM hr.payroll_lines)                   pl_runid_sum,
 (SELECT SUM(CAST(net        AS DECIMAL(38,2))) FROM hr.payroll_lines)                pl_net,
 (SELECT SUM(CAST(revenue_usd AS DECIMAL(38,2))) FROM finance.monthly_summary)        msum_rev,
 (SELECT SUM(CAST(wages_usd   AS DECIMAL(38,2))) FROM finance.monthly_summary)        msum_wages,
 (SELECT SUM(CAST(line_total  AS DECIMAL(38,2))) FROM dw.fact_sales)                  dw_fact,
 (SELECT COUNT_BIG(*) FROM dbo.releases
    WHERE MONTH(first_release_date)=1 AND DAY(first_release_date)=1)                  jan1_fb,
 (SELECT COUNT_BIG(*) FROM dbo.transactions t
    WHERE t.staff_id=10 AND t.total<0
      AND t.shop_id=(SELECT shop_id FROM dbo.shops WHERE shop_code='US-0009'))        fraud_n;
```
</details>

<details>
<summary><strong>PostgreSQL</strong></summary>

```sql
SELECT
 (SELECT COUNT(*) FROM public.transactions)                                           tx,
 (SELECT COUNT(*) FROM public.transaction_lines)                                      lines,
 (SELECT COUNT(*) FROM public.payments)                                               pays,
 (SELECT COUNT(*) FROM public.inventory_movements)                                    movs,
 (SELECT COUNT(*) FROM public.trade_ins)                                              tradeins,
 (SELECT COUNT(*) FROM public.customers)                                              custs,
 (SELECT SUM(CAST(total      AS NUMERIC(38,2))) FROM public.transactions)             tx_total,
 (SELECT SUM(CAST(line_total AS NUMERIC(38,2))) FROM public.transaction_lines)        line_total,
 (SELECT SUM(CAST(quantity   AS BIGINT))        FROM public.transaction_lines)        qty,
 (SELECT COUNT(*) FROM web.reviews)                                                   reviews,
 (SELECT SUM(CAST(rating AS BIGINT)) FROM web.reviews)                                rating_sum,
 (SELECT COUNT(*) FROM web.page_views)                                                page_views,
 (SELECT COUNT(*) FROM hr.payroll_runs)                                               pr_runs,
 (SELECT SUM(CAST(payroll_run_id AS BIGINT)) FROM hr.payroll_lines)                   pl_runid_sum,
 (SELECT SUM(CAST(net        AS NUMERIC(38,2))) FROM hr.payroll_lines)                pl_net,
 (SELECT SUM(CAST(revenue_usd AS NUMERIC(38,2))) FROM finance.monthly_summary)        msum_rev,
 (SELECT SUM(CAST(wages_usd   AS NUMERIC(38,2))) FROM finance.monthly_summary)        msum_wages,
 (SELECT SUM(CAST(line_total  AS NUMERIC(38,2))) FROM dw.fact_sales)                  dw_fact,
 (SELECT COUNT(*) FROM public.releases
    WHERE EXTRACT(MONTH FROM first_release_date)=1
      AND EXTRACT(DAY FROM first_release_date)=1)                                     jan1_fb,
 (SELECT COUNT(*) FROM public.transactions t
    WHERE t.staff_id=10 AND t.total<0
      AND t.shop_id=(SELECT shop_id FROM public.shops WHERE shop_code='US-0009'))     fraud_n;
```
</details>

## Expected values

Tab-separated, in the column order above, so you can diff your query's output
directly against the matching line.

**SQL Server**

```
tiny     3107557     4770413     3145015     5051034    209517     39434      196789123.52     196789123.52     4621743     7826     26020     331589     1594   32513584     43397514.82     197860354.93      92068039.64    196789123.52     15929   0
small    16001627    24653685    16215545    26219633   1172630    393990     2399241395.89    2399241395.89    23878062    114591   391787    3140829    6522   711122815    274788308.56    1132869607.72     239752598.95   2399241395.89    15929   0
medium   144863331   224380820   147087492   240108485  11825689   3940144    69267716335.40   69267716335.40   217206855   1271255  4337882   31223093   7577   6036026402   4099038827.83   11064975480.50    1570369575.90  69267716335.40   15929   2972
large    1356470465  2101970283  1377101078  2250673092 111860307  39399217   737665127412.58  737665127412.58  2034646565  12550447 42865404  312037761  8122   61886248772  49448652298.71  104705485807.51   14586434747.60 737665127412.58  15929   2972
```

**PostgreSQL** (identical to SQL Server at every tier **except `msum_wages`** — see note)

```
tiny     3107557     4770413     3145015     5051034    209517     39434      196789123.52     196789123.52     4621743     7826     26020     331589     1594   32513584     43397514.82     197860354.93      92068039.66    196789123.52     15929   0
small    16001627    24653685    16215545    26219633   1172630    393990     2399241395.89    2399241395.89    23878062    114591   391787    3140829    6522   711122815    274788308.56    1132869607.72     239752599.07   2399241395.89    15929   0
medium   144863331   224380820   147087492   240108485  11825689   3940144    69267716335.40   69267716335.40   217206855   1271255  4337882   31223093   7577   6036026402   4099038827.83   11064975480.50    1570369576.14  69267716335.40   15929   2972
large    1356470465  2101970283  1377101078  2250673092 111860307  39399217   737665127412.58  737665127412.58  2034646565  12550447 42865404  312037761  8122   61886248772  49448652298.71  104705485807.51   14586434749.15 737665127412.58  15929   2972
```

## Notes

- **`msum_wages` is the only cross-engine difference.** SQL Server is a hair lower
  at every tier — 2¢ (tiny), 12¢ (small), 24¢ (medium), $1.55 (large). This is an
  inherent decimal-division rounding difference: the monthly wage rollup computes
  `SUM(annual_wage / 12 / fx_rate)`, and SQL Server's `decimal/decimal` uses a
  smaller intermediate scale than PostgreSQL's `numeric/numeric`. The **source
  data is byte-identical** — `dw_fact = line_total = tx_total` to the penny on
  both engines — and `msum_rev` (the revenue rollup) matches exactly. It is a
  derived, recomputable value, and a genuine "same query, two engines, different
  precision rule" teaching point rather than a defect.
- **`fraud_n` is 0 on `tiny`/`small`.** A couple of canon "anomalies" (including
  an embedded fraud pocket) are gated to the full-scale tiers; the compact
  extracts don't carry them, so those two tiers report `0` where `medium`/`large`
  report `2972`. Everything else scales with the tier.
- **`jan1_fb` is 15929 on every tier.** It counts catalogue titles that genuinely
  released on 1 January; the catalogue is shared across tiers, so the number is
  constant. (It is *not* a count of missing dates — those were corrected.)
