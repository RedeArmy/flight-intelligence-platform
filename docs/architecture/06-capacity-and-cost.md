# 06 — Capacity and Cost Model

No real traffic or provider pricing exists yet. This is a **parametric model**: plug measured values in.
All example numbers are illustrative placeholders, not forecasts or provider facts.

## 1. Provider request model

```
provider_requests/day =
    user_searches/day × providers_per_search × (1 + retry_rate)
  + monitored_targets × (24h / tier_interval) × providers_per_search × (1 + retry_rate)
  + verifications/day × (1 + retry_rate)
  + monitoring_verifications/day
```

Binding constraint is typically **provider rate limits and per-call cost**, not our infrastructure.
Per-provider daily/second budget (from contract) caps the sum; the scheduler allocates budget to tiers
(CRITICAL first) and sheds LOW first.

Illustrative sizing worksheet (placeholders):

| Input | Example | Output |
|-------|---------|--------|
| User searches/day | 20,000 | |
| Providers/search | 3 | 60,000 provider calls |
| Monitored targets | 5,000 | |
| Tier mix (LOW 24h / NORMAL 6h / HIGH 1h / CRIT 15m) | 70/20/8/2 % | ≈ 3,500×1 + 1,000×4 + 400×24 + 100×96 = 26,700 searches/day ×3 providers = 80,100 calls |
| Verifications/day | 5,000 | 5,000 calls |
| **Total** | | ≈ 145k provider calls/day ≈ 1.7/s average |

Peak factor 5–10× on user traffic; monitoring is smoothed by the scheduler (jitter, budget).

## 2. Storage model

`observations/day ≈ offers_returned_per_search × searches_total/day` (before dedup) — can be large;
levers: store only deduped canonical offers per (offer, time bucket), downsample low-tier routes,
retain raw only when permitted. Row size estimate per price observation ~150–250 B + index overhead;
re-estimate with real distributions. Partition/archive decision triggered by measurement
(e.g. table > 100 GB or p95 history latency breaching SLO), not before.

## 3. Cost model (track from first provider call)

```
Cost per search = Σ provider_request_cost + compute_ms × rate + egress + storage_writes + processing
Cost per verification, per monitored route-month, per alert, per observation: same decomposition
```

Metrics: `provider_cost_estimate_total` (needs per-provider unit price config), infra cost via cloud
billing tags (`env`, `service`, `provider`), unit-economics report monthly. Provider routing may
eventually weigh cost (deterministic, observable; Constitution §60).

## 4. Capacity guardrails

Concurrency limits per provider, global search concurrency cap, queue depth alarms, DB connection
pool sizing (pgx pool ≤ instances × N), autoscale API on CPU/latency, workers on queue depth/age. Load
tests (k6 or equivalent, ADR in E1) with mock providers inject latency/failure to validate degradation.
Revisit queue/DB/infra only when a measured SLO or cost threshold is breached (ADR-001 extraction criteria).
