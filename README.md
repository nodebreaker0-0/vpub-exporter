# vpub-exporter

A read-only Prometheus exporter for [hl-validator-publisher](https://github.com/hyperliquid-dex/node) — co-located on the same host, scraped by your existing monitoring stack, and emits 26 alert rules covering process, child-component, RPC quorum, bridge/oracle vote freshness, Slack health, and per-component binary upgrade tracking.

Built by [B-Harvest](https://b-harvest.io) for our own validator deployment on Hyperliquid testnet and mainnet. Shared as-is; PRs welcome.

> **Read-only by design.** The exporter only `stat`s and `HEAD`s — it never writes to the publisher's directory, never sends votes, never restarts services. The whole point is to monitor the publisher safely without becoming an automation foot-gun.

---

## What it watches

| Tier | Signal | Examples |
|---|---|---|
| **Tier 0** | process & log liveness | `vpub_service_up`, `vpub_child_count`, `vpub_component_log_mtime_seconds` |
| **Tier 1** | bridge / oracle / outcome behavior | `vpub_bridge_rpc_up{name=...}`, `vpub_bridge_last_vote_success_unix`, `vpub_oracle_vote_total`, `vpub_outcome_slack_msg_24h`, `vpub_slack_api_ok` |
| **Tier 2** | per-component binary upgrade | `vpub_binary_local_mtime_unix{component=...}`, `vpub_binary_remote_mtime_unix{component="visor"}`, `vpub_binary_download_started_unix{component=...}` |

All metric names use the `vpub_` prefix. `instance` / `chain` / `network` labels are injected by the Prometheus side, not the exporter.

Full metric catalogue: [`specs/001-vpub-exporter/contracts/metrics.md`](specs/001-vpub-exporter/contracts/metrics.md).

---

## Quickstart

### Prerequisites

- The publisher is already running on the same machine (`systemctl status validator-publisher` → `active`)
- Linux amd64, dedicated host (Tokyo recommended for latency)
- User `admin` (testnet) or `ubuntu` (mainnet) — same user that runs the publisher
- A Slack bot token + outcome-channel ID + Arbitrum RPC URLs — reuse what the publisher's `config.json` already holds

### Build & install

Three paths — release binary is the fastest:

#### Option A — release binary (recommended)

```bash
mkdir -p ~/vpub-exporter/bin
cd ~/vpub-exporter

# Pull the latest release + sha256 + verify
curl -L https://github.com/nodebreaker0-0/vpub-exporter/releases/latest/download/vpub-exporter-linux-amd64 \
  -o bin/vpub-exporter
curl -L https://github.com/nodebreaker0-0/vpub-exporter/releases/latest/download/vpub-exporter-linux-amd64.sha256 \
  | sha256sum -c -
chmod +x bin/vpub-exporter

sudo install -D -m 0755 bin/vpub-exporter /home/$USER/vpub-exporter/bin/vpub-exporter
```

#### Option B — build on the publisher host

```bash
# Go 1.21+
go version || (echo "Install Go 1.21+ from https://go.dev/dl"; exit 1)

git clone https://github.com/nodebreaker0-0/vpub-exporter.git
cd vpub-exporter
make build-linux           # → bin/vpub-exporter-linux-amd64 (statically linked ELF)
sha256sum bin/vpub-exporter-linux-amd64

sudo install -D -o admin -g admin -m 0755 \
  bin/vpub-exporter-linux-amd64 \
  /home/admin/vpub-exporter/bin/vpub-exporter
```

#### Option C — cross-compile elsewhere, scp to host

```bash
# Dev box (macOS / Linux)
git clone https://github.com/nodebreaker0-0/vpub-exporter.git
cd vpub-exporter
make build-linux
scp bin/vpub-exporter-linux-amd64 admin@<PUBLISHER_IP>:/tmp/

# Publisher host
ssh admin@<PUBLISHER_IP>
sudo install -D -o admin -g admin -m 0755 \
  /tmp/vpub-exporter-linux-amd64 \
  /home/admin/vpub-exporter/bin/vpub-exporter
```

### Configure

```bash
# 1) Drop the env template — every value below is required for the matching collector
curl -L https://raw.githubusercontent.com/nodebreaker0-0/vpub-exporter/main/env/vpub-exporter.env.example \
  -o /tmp/vpub.env.example
sudo cp /tmp/vpub.env.example /etc/vpub-exporter.env
sudo chmod 0600 /etc/vpub-exporter.env
sudo $EDITOR /etc/vpub-exporter.env    # fill Slack token, RPC URLs, etc.

# 2) systemd unit
curl -L https://raw.githubusercontent.com/nodebreaker0-0/vpub-exporter/main/systemd/vpub-exporter.service \
  -o /tmp/vpub.service
sudo cp /tmp/vpub.service /etc/systemd/system/vpub-exporter.service

# 3) Start
sudo systemctl daemon-reload
sudo systemctl enable --now vpub-exporter

# 4) Verify
sleep 30
curl -s localhost:8002/metrics | grep '^vpub_' | head -20
```

You should see at least:

```
vpub_service_up 1
vpub_child_count 3
vpub_binary_local_mtime_unix{component="visor"} 1.77...e+09
vpub_binary_local_mtime_unix{component="bridge-voter"} 1.77...e+09
vpub_binary_remote_check_ok{component="visor"} 1
vpub_bridge_rpc_up{name="alchemy"} 1
...
```

### Environment variables

| Key | Purpose | Example |
|---|---|---|
| `VPUB_VISOR_LOG_DIR` | visor's own log directory (the `--log-dir` argument) | `/home/admin/v-publisher/log` (testnet) / `/home/ubuntu/v-publisher/log` (mainnet) |
| `VPUB_COMPONENT_LOG_DIR` | root of bridge-voter / oracle / outcome-voter subdirs | `/tmp/validator-publisher` (both networks — visor hardcodes this) |
| `VPUB_BINARY_TARGETS` | per-component binary paths for Tier 2 tracking | `visor=/home/admin/v-publisher/visor,bridge-voter=...,outcome-voter=...,reference-oracle-publisher=...` |
| `VPUB_BINARY_URL` | HF visor announce URL (`HEAD` polled for upgrade tracking) | `https://binaries.hyperliquid-testnet.xyz/validator-publisher/visor` |
| `VPUB_BRIDGE_STATE_PATH` | bridge-voter state JSON (read-only progress signal) | `/home/admin/v-publisher/bridge-voter-testnet-state.json` |
| `VPUB_BRIDGE_RPC_NAMES` | comma-separated Arbitrum RPC provider names (lowercase) | `alchemy,quicknode,infura` (testnet 3) or 7 names (mainnet) |
| `VPUB_RPC_<NAME>_URL` | URL per provider (secret) | `https://arb-sepolia.g.alchemy.com/v2/<KEY>` |
| `VPUB_SLACK_BOT_TOKEN` | Slack bot token (same one the publisher uses) | `xoxb-...` |
| `VPUB_OUTCOME_CHANNEL` | outcome_actions Slack channel ID | `C0XXXXXX` |
| `VPUB_*_PATTERNS` | log regex overrides (newline-separated) | sensible defaults shipped in env.example |

Mainnet uses `env/vpub-exporter.env.mainnet.example` as a starting point.

### CLI flags

```text
  --listen-addr=:8002              HTTP listen address for /metrics
  --service-name=validator-publisher.service   systemd unit to monitor
  --scrape-interval=30s            default tick interval
  --shutdown-timeout=10s
```

---

## Architecture

```
                    ┌─────────────────────────┐
                    │  Prometheus / monitor   │
                    │  (your existing stack)  │
                    └───────────┬─────────────┘
                                │ scrape :8002/metrics
                                ▼
┌───────────────────────────────────────────────────┐
│  publisher host                                   │
│  ┌──────────────────┐    ┌──────────────────┐    │
│  │ validator-pub    │    │ vpub-exporter    │    │
│  │ .service         │    │ .service         │    │
│  │  • visor         │    │  • collectors    │    │
│  │  • bridge-voter  │    │  • read-only     │    │
│  │  • outcome-voter │    │  • promhttp      │    │
│  │  • ref-oracle    │    │  → systemd dbus  │    │
│  └────┬─────────────┘    └──┬───────────────┘    │
│       │ writes logs         │ stats / HEAD       │
│       ▼                     ▼                    │
│  /home/<user>/v-publisher/        ← read-only    │
│  /tmp/validator-publisher/        ← read-only    │
└───────────────────────────────────────────────────┘
```

The exporter co-locates with the publisher because every signal it cares about — log freshness, child PID, binary mtime, bridge state JSON — is a file on the same host. CPU and memory cost is negligible (RSS ≈ 15MB, p95 `/metrics` latency ≈ 1.4ms in the testnet baseline).

---

## Alert rules

30 rules, deployed via [`monitoring/rules/hyperliquid_vpub_rule.yaml`](monitoring/rules/hyperliquid_vpub_rule.yaml) (Tier 0 = 11 / Tier 1 = 16 / Tier 2 = 3 — R-019 per-component binary tracking + R-022 visor restart-loop detection). Full spec: [`specs/001-vpub-exporter/contracts/alerts.md`](specs/001-vpub-exporter/contracts/alerts.md).

### alertLevel routing (B-Harvest convention)

| `alertLevel` | Channel |
|---|---|
| `critical` | PagerDuty + `#ddoa-critical` |
| `high` | `#ddoa-high` |
| `medium`, `low` | `#ddoa-low` |

### Testnet / mainnet split + alertLevel policy (R-020)

`critical` is reserved for **silent-failure** modes that the operator would never otherwise notice — exactly three rules: `VpubServiceDown`, `VpubChildMissing`, `VpubSlackTokenInvalid`. Everything else that used to be critical is now `high` (still actioned, but not paged at 3am for a wedge that auto-resolves).

Testnet siblings of every alert run at `alertLevel: low` to keep `#ddoa-high` quiet — the same conditions still fire so operators have visibility without paging stress.

**R-020 mainnet temporary state (2026-05-24)**: HF announced mainnet validator-publisher is live, but `bridge_voter` and `reference_oracle_publisher` are disabled until the next L1 upgrade. Five rules are silenced (threshold widened to 30d) so the disabled subsystems don't permanently fire: `VpubBridgeStaleVote{,Long}`, `VpubBridgeStateStuck`, `VpubOracleStaleVote{,Long}`. Restore procedure documented in `specs/001-vpub-exporter/research.md § R-020`.

> **Slack template note.** The B-Harvest alarmer renders only the alert `summary` in Slack messages. Put timestamps, URLs, and `humanizeTimestamp` / `humanizeDuration` in `summary`; keep `description` for the Prometheus `/alerts` API.

### Examples

| Rule | Fires when | Operator next step |
|---|---|---|
| `VpubServiceDown` (critical, mainnet) | `vpub_service_up == 0` for 30s | `journalctl -u validator-publisher`; restart? incident? |
| `VpubChildMissing` (critical, mainnet) | `min_over_time(vpub_child_count[5m]) < 3` — restart-loop dips counted (R-022a) | visor spawn loop broken — see visor log |
| `VpubVisorChildRestartLoop` (critical, mainnet) | visor logs `restarting process` 5+ times in 5m (R-022b) | version skew / spawn error — `journalctl -u validator-publisher` + visor log |
| `VpubVisorCrit` (critical, mainnet) | visor emits a `CRIT`/`ERROR` line (R-022b) | early warning before child or visor exit — read the matched line and act |
| `VpubBridgeRpcMajorityDown` (high) | live RPCs < 4 (mainnet) / < 2 (testnet) — HF: 4/7 quorum (R-005c) | rotate RPC keys or add provider |
| `VpubBridgeRpcDisagreement` (high) | 5m window has 2+ disagreements — HF: ≤1 per vote (R-005c) | one RPC giving different answers — Sybil / key compromise / indexer bug |
| `VpubBridgeStaleVote{,Long}` (high) | last successful bridge vote older than 30m / 2h (R-021 jail-safe, mainnet only) | Arbitrum RPC issue or bridge_voter stuck — jail risk |
| `VpubOracleStaleVote{,Long}` (high) | last successful oracle vote older than 1m / 5m (R-021 jail-safe, mainnet only) | reference_oracle stuck — jail risk |
| `VpubVisorBinaryUpdateAvailable` (medium) | HF published a newer `/validator-publisher/visor` | review changelog, install manually |
| `VpubChildBinaryDownloadFailed` (high) | visor logged "downloading new binary" but the child file's mtime didn't catch up within 60s | visor's `maybe_download` is stuck — check network / disk |
| `VpubSlackTokenInvalid` (critical, mainnet) | Slack `auth.test` failing for 5m | **the publisher's own Slack alerts may be missing too** |

> Mainnet bridge_voter / reference_oracle are temporarily disabled by HF (pending L1 upgrade). The five corresponding rules (`VpubBridgeStaleVote{,Long}`, `VpubBridgeStateStuck`, `VpubOracleStaleVote{,Long}`) ship with `> 30d` thresholds as a soft silence; restore to the R-021 jail-safe values above the moment the L1 upgrade lands. Restore script in `specs/001-vpub-exporter/research.md § R-020`.

---

## Mainnet checklist

The exporter is built so a single binary serves both networks. Differences are absorbed by env files and a systemd drop-in — no code branch.

**Prep (when HF announces the mainnet binary URL, R-006):**

1. Copy `env/vpub-exporter.env.mainnet.example` to `/etc/vpub-exporter.env`. Fill: `/home/ubuntu/v-publisher` paths, 7 Arbitrum RPCs, Slack token, channel ID, R-006 binary URL.
2. Install the `ubuntu`-user drop-in (raises MemoryMax to 400M for the ~10× mainnet log throughput):
   ```bash
   sudo install -D -m 0644 systemd/mainnet.conf \
     /etc/systemd/system/vpub-exporter.service.d/mainnet.conf
   sudo systemctl daemon-reload
   ```
3. Add `monitoring/agents/Main_hyperliquid_VPUB_<region>.toml` (replace `<MAINNET_IP>`) and open a PR against your monitoring repo.
4. Cross-compile or build on the host (same `make build-linux`); install under `/home/ubuntu/vpub-exporter/bin/vpub-exporter`.
5. **PagerDuty dry-run** before going live — temporarily route `alertLevel="critical"` to a test channel, stop the publisher for 30s, confirm `VpubServiceDown` fires, then restore the PagerDuty route. See `specs/001-vpub-exporter/quickstart.md § QS-2.1p`.

**Right after starting on mainnet:**

6. `sudo systemctl enable --now vpub-exporter`
7. `curl localhost:8002/metrics | grep '^vpub_'` — confirm all 4 `vpub_binary_local_mtime_unix{component=...}` series plus `vpub_binary_remote_check_ok{component="visor"} == 1`
8. `vpub_bridge_rpc_up == 1` count should equal 7
9. In the monitoring stack: `curl <prom>:9090/api/v1/targets` — the new instance should be `up`

**First 24 hours — burst check at T+1h / T+6h / T+24h:**

```bash
bash scripts/mainnet_burst_check.sh
# Verdict: PASS / FAIL with per-metric breakdown
```

Targets: `/metrics` p95 < 200ms · RSS < 400MB · CPU < 20% · `collection_duration_seconds` p95 < 1s · RPC up count ≥ 4.

**After the first real vote (R-005 / R-017c):**

10. Measure the actual RPC quorum HF requires by selectively blocking RPCs in a quiet window — adjust `VpubBridgeRpcMajorityDown` threshold if it differs from the `< 4` assumption.
11. Measure the average inter-vote interval and set `VpubBridgeStaleVoteLong` threshold to roughly `max(5 × interval, 2h)`.
12. After a week of operation: `bash scripts/sc007_eval.sh` to evaluate false-positive rate against SC-007 (< 10%).

---

## Verification gate

Before any merge:

```bash
make verify    # = vet + test -race + promtool check + secrets-leak + constitution-gate
```

What each step does:

- `make vet` — `go vet ./...`
- `make test` — unit + integration tests, race-enabled
- `make promtool-check` — every rule yaml is wrapped in `{groups: [.]}` and checked with `promtool check rules` to mirror what the monitoring repo's parser does at merge
- `make secrets-leak` — three tests that inject fake tokens, then assert no `xoxb-...`, no 32-byte hex keys, and no embedded secrets in any committed `.go` / `.yaml` / `.md` / `.toml` file
- `make constitution-gate` — six greps for read-only invariants, write-mode `O_WRONLY` opens, dependency count, hardcoded secrets, alertLevel domain, and HTTP client timeouts

If `verify` is green the change is safe to push. The same target runs in CI on every PR.

---

## Repository layout

```
vpub-exporter/
├── cmd/vpub-exporter/main.go
├── internal/
│   ├── binary/         HTTP HEAD + os.Stat probe
│   ├── collectors/     12 collectors (service, logmtime, bridge_rpc, bridge_state,
│   │                   vote_logs, outcome_logs, outcome_slack, slack_health,
│   │                   binary, download_logs, visor_log)
│   ├── config/         env + flag loading
│   ├── logfs/          log dir / file enumeration
│   ├── logtail/        polling tailer with rotation awareness
│   ├── procs/          /proc-based child PID enumeration
│   ├── rpc/            Arbitrum JSON-RPC client (eth_blockNumber only)
│   ├── slackapi/       Slack auth.test + conversations.history
│   └── systemd/        dbus probe (UnitProperties + ServiceProperties split)
├── tests/integration/  full /metrics + secrets-leak regression
├── monitoring/
│   ├── agents/         per-instance TOML for the B-Harvest monitoring repo
│   └── rules/          tier0 / tier1 / tier2 yaml + merged superset
├── systemd/            vpub-exporter.service + mainnet.conf drop-in
├── env/                env.example (testnet) + env.mainnet.example
├── scripts/
│   ├── sc007_eval.sh             7-day false-positive harness
│   └── mainnet_burst_check.sh    T+1h / 6h / 24h verdict
└── specs/001-vpub-exporter/      spec-kit: charter, plan, research, tasks, contracts
```

---

## Design principles

1. **Read-only.** No writes to the publisher's filesystem. No automated actions on the network — voting, restarting, unjailing are operator decisions.
2. **Single binary, two networks.** Differences live in `env` and a systemd drop-in. No build-time network flag.
3. **Spec-driven.** [`specs/001-vpub-exporter/`](specs/001-vpub-exporter/) is the source of truth — code changes flow from a spec change, not the other way around. Operational discoveries get backported as R-### entries in `research.md`.
4. **Dependencies minimal.** Three direct Go deps: `prometheus/client_golang`, `coreos/go-systemd/v22`, `godbus/dbus/v5`. Anything new requires a Constitution III review.
5. **Auto-resolve where possible.** Most alerts have an expression that goes false when the underlying condition recovers (mtime catches up, RPC comes back, Slack re-auths). The operator sees `firing` followed by `resolved` without having to acknowledge.
6. **Verify before merge.** `make verify` is the gate. Everything else is convention.

See [`.specify/memory/constitution.md`](.specify/memory/constitution.md) for the full 7-principle constitution.

---

## Korean / 한국어

The original Korean operational notes are preserved in [`README.ko.md`](README.ko.md). The Korean version carries more inline context about the B-Harvest deployment specifically (Tokyo host, validator-publisher.service, ddoa-* channels). The English README above is the canonical one going forward.

---

## License & contributing

PRs welcome. Issues against the spec are most useful when they cite the specific FR-### or R-### they affect — that keeps spec and code in sync.
