# vpub-exporter

> Read-only Prometheus exporter for [hl-validator-publisher](https://binaries.hyperliquid-testnet.xyz/validator-publisher/README.md) (Hyperliquid).
>
> publisher 가 보내는 Slack 알람만으로는 publisher 자체가 죽었을 때 통보가 안 옴. vpub-exporter 는 같은 머신에서 **외부에서** publisher 의 상태를 관찰해서 진짜로 살아있는지, 일을 하고 있는지를 Prometheus 메트릭으로 노출한다. publisher 의 파일을 **절대 수정하지 않는다** (read-only).
>
> spec-kit 컨벤션 — 전체 설계는 [`specs/001-vpub-exporter/`](specs/001-vpub-exporter/) 안.

---

## 목차

- [무엇을 보는가](#무엇을-보는가)
- [Quickstart — 가장 쉬운 설치](#quickstart--가장-쉬운-설치)
- [Configuration](#configuration)
- [Metrics — 메트릭 전체 설명](#metrics--메트릭-전체-설명)
- [Alert Rules — 언제 어떤 알람이 울리고, 울리면 뭘 봐야 하나](#alert-rules--언제-어떤-알람이-울리고-울리면-뭘-봐야-하나)
- [Architecture](#architecture)
- [Build (개발자)](#build-개발자)
- [Constitution & Safety](#constitution--safety)
- [Links](#links)

---

## 무엇을 보는가

publisher 는 한 머신에서 **`visor` 가 3 자식 프로세스를 spawn** 하는 구조:

```
validator-publisher.service (systemd)
└── visor
    ├── bridge-voter                  ← Arbitrum 입금 모니터링 + L1 bridge vote
    ├── reference-oracle-publisher    ← 가격 oracle update vote
    └── outcome-voter                 ← outcome 마켓 deploy/settle 검토 알림
```

vpub-exporter 가 보는 3 가지 카테고리:

| Tier | 무엇 | 데이터 소스 |
|---|---|---|
| **Tier 0 (MVP)** | "publisher 가 진짜 살아서 일하고 있나" | systemd dbus + `/proc/<pid>/task` + 각 컴포넌트 로그 디렉토리 mtime |
| **Tier 1** | "각 컴포넌트가 본업을 끝까지 가나" | Arbitrum RPC (eth_blockNumber), 컴포넌트 로그 패턴 매칭, Slack API (`auth.test`, `conversations.history`), `bridge-voter-<chain>-state.json` |
| **Tier 2** | "새 publisher 바이너리가 announced 됐나" | HTTP HEAD `Last-Modified` |

---

## Quickstart — 가장 쉬운 설치

### 0. 사전 조건

- publisher 가 같은 머신에서 이미 가동 중 (`systemctl status validator-publisher` → active)
- Linux amd64 머신 (Tokyo / dedicated 권장)
- `admin` (testnet) 또는 `ubuntu` (mainnet) user
- Slack bot token + outcome 채널 ID + Arbitrum RPC URL 들 (publisher 의 `config.json` 과 동일한 값 재사용)

### 방법 A — release binary 다운로드 (가장 빠름)

> 운영자가 5분 안에 깔고 띄울 수 있는 경로.

```bash
# 1) release binary 받기
mkdir -p ~/vpub-exporter/bin
cd ~/vpub-exporter
curl -L https://github.com/nodebreaker0-0/vpub-exporter/releases/latest/download/vpub-exporter-linux-amd64 \
  -o bin/vpub-exporter
chmod +x bin/vpub-exporter

# 2) env 파일 다운로드 + 채움 (시크릿)
curl -L https://raw.githubusercontent.com/nodebreaker0-0/vpub-exporter/main/env/vpub-exporter.env.example \
  -o /tmp/vpub.env.example
sudo cp /tmp/vpub.env.example /etc/vpub-exporter.env
sudo chmod 0600 /etc/vpub-exporter.env
sudo nano /etc/vpub-exporter.env    # Slack token, RPC URL 들 채우기

# 3) systemd unit 다운로드 + 설치
curl -L https://raw.githubusercontent.com/nodebreaker0-0/vpub-exporter/main/systemd/vpub-exporter.service \
  -o /tmp/vpub.service
sudo cp /tmp/vpub.service /etc/systemd/system/vpub-exporter.service
sudo systemctl daemon-reload
sudo systemctl enable --now vpub-exporter

# 4) 동작 확인
sleep 30
curl -s localhost:8002/metrics | grep '^vpub_' | head -20
```

### 방법 B — 소스에서 직접 빌드

> 보안 감사가 필요하거나 코드 검토 후 자기 손으로 컴파일하고 싶을 때.

```bash
# 1) 빌드 도구 (Go 1.21+)
go version || (echo "Install Go 1.21+ from https://go.dev/dl"; exit 1)

# 2) clone + build
git clone https://github.com/nodebreaker0-0/vpub-exporter.git
cd vpub-exporter
make build-linux           # → bin/vpub-exporter-linux-amd64 (ELF, statically linked)
sha256sum bin/vpub-exporter-linux-amd64

# 3) 설치 (위 A 방법의 1번 대신)
sudo install -D -o admin -g admin -m 0755 \
  bin/vpub-exporter-linux-amd64 \
  /home/admin/vpub-exporter/bin/vpub-exporter

# 이후 단계는 A 의 2~4 와 동일.
```

### 방법 C — 개발 머신에서 cross-compile 후 publisher 머신에 scp

> 운영자가 개발 머신 (macOS) 에서 빌드해서 Linux publisher 에 배포할 때.

```bash
# 개발 머신 (macOS)
git clone https://github.com/nodebreaker0-0/vpub-exporter.git
cd vpub-exporter
make build-linux
scp bin/vpub-exporter-linux-amd64 admin@<PUBLISHER_IP>:/tmp/

# publisher 머신
ssh admin@<PUBLISHER_IP>
sudo install -D -o admin -g admin -m 0755 \
  /tmp/vpub-exporter-linux-amd64 \
  /home/admin/vpub-exporter/bin/vpub-exporter

# 위 A 의 2~4 와 동일하게 env / systemd / start
```

### 가동 첫 확인

```bash
systemctl status vpub-exporter --no-pager
journalctl -u vpub-exporter -n 30 --no-pager

curl -s localhost:8002/metrics | grep -E '^vpub_(service_up|child_count|component_log_mtime|bridge_state) '
# 기대:
#   vpub_service_up 1
#   vpub_child_count 3
#   vpub_component_log_mtime_seconds{component="bridge-voter"} 1.7795e+09
#   vpub_component_log_mtime_seconds{component="outcome-voter"} 1.7795e+09
#   vpub_component_log_mtime_seconds{component="reference-oracle-publisher"} 1.7795e+09
#   vpub_component_log_mtime_seconds{component="visor"} 1.7795e+09
#   vpub_bridge_state_last_scanned_block 2.705e+08
```

---

## Configuration

모든 환경변수는 `/etc/vpub-exporter.env` (`EnvironmentFile=`) 으로 주입. 시크릿은 코드/메트릭/로그 어디에도 노출하지 않는다 (Constitution IV).

자세한 예시: [`env/vpub-exporter.env.example`](env/vpub-exporter.env.example).

| 변수 | 무엇 | 예시 |
|---|---|---|
| `VPUB_VISOR_LOG_DIR` | visor 자체 로그 디렉토리 (service 파일의 `--log-dir` 값) | `/home/admin/v-publisher/log` (testnet) / `/home/ubuntu/v-publisher/log` (mainnet) |
| `VPUB_COMPONENT_LOG_DIR` | 3 child 컴포넌트 로그 루트 (visor default — `--log-dir` 영향 X) | `/tmp/validator-publisher` (양쪽 동일, system /tmp) |
| `VPUB_BINARY_PATH` | publisher 바이너리 (Tier 2 upgrade tracking) | `/home/admin/v-publisher/visor` |
| `VPUB_BRIDGE_STATE_PATH` | bridge-voter state JSON (read-only) | `/home/admin/v-publisher/bridge-voter-testnet-state.json` |
| `VPUB_BRIDGE_RPC_NAMES` | Arbitrum RPC provider 이름 콤마 구분 (lowercase) | `alchemy,quicknode,infura` (testnet 3) / 7개 (mainnet) |
| `VPUB_RPC_<NAME>_URL` | 각 provider URL (시크릿) | `https://arb-sepolia.g.alchemy.com/v2/...` |
| `VPUB_SLACK_BOT_TOKEN` | Slack bot token (publisher 의 `config.json` 의 그것 재사용) | `xoxb-...` |
| `VPUB_OUTCOME_CHANNEL` | outcome_actions Slack channel ID | `C0XXXXXX` |
| `VPUB_BINARY_URL` | HF announced publisher binary URL | testnet: `https://binaries.hyperliquid-testnet.xyz/validator-publisher/visor` |
| `VPUB_*_PATTERNS` | 로그 패턴 정규식 (newline-separated) | env.example 에 testnet 9.7h 실관찰 default 포함 |

### Flags

```text
--listen-addr        :8002 (default)
--scrape-interval    30s
--service-name       validator-publisher.service
```

### systemd unit ([`systemd/vpub-exporter.service`](systemd/vpub-exporter.service))

핵심 보안 설정:
- `User=admin` / `Group=admin` (publisher 와 동일 user — log read 권한)
- `ProtectSystem=full` / `ProtectHome=read-only` / `ReadOnlyPaths=/home/admin/v-publisher` — publisher 파일 변형 차단
- `PrivateTmp=no` — publisher 의 `/tmp/validator-publisher/` 컴포넌트 로그 read 위해 필수
- `NoNewPrivileges` / `MemoryMax=200M` / `CPUQuota=20%` — Constitution Operational Constraints

---

## Metrics — 메트릭 전체 설명

prefix: 모든 메트릭은 **`vpub_*`**.

라벨 정책: `instance` / `chain` / `network` 라벨은 Prometheus / monitoring 레포의 agent TOML 이 자동 주입 — 메트릭 자체에는 명시 X. `vpub_` prefix 가 unique 해서 다른 모니터링 대상과 충돌 없음.

### A. Tier 0 — 프로세스 / 진행성

#### `vpub_service_up`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (0/1) |
| **무엇** | systemd `validator-publisher.service` 가 active 인지 |
| **어떻게** | systemd dbus `GetUnitPropertiesContext` → `ActiveState == "active"` |
| **주기** | 5초 |
| **코드** | [`internal/collectors/service.go`](internal/collectors/service.go), [`internal/systemd/systemd_dbus.go`](internal/systemd/systemd_dbus.go) |
| **운영 의미** | `0` 이면 publisher 가 죽었다는 가장 강한 시그널. |

#### `vpub_child_count`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (int) |
| **무엇** | visor 가 spawn 한 child process 개수 (정상 = 3: bridge-voter / reference-oracle-publisher / outcome-voter) |
| **어떻게** | dbus 로 받은 visor `MainPID` (Service interface) → `/proc/*/stat` 의 PPID 가 그 값인 PID 카운트 |
| **주기** | 10초 |
| **코드** | [`internal/procs/procs_proc.go`](internal/procs/procs_proc.go), [`internal/collectors/service.go`](internal/collectors/service.go) |
| **운영 의미** | publisher 가 robust 해서 child 가 죽으면 visor 가 거의 즉시 재spawn. 따라서 이 값이 < 3 이 30초 이상 지속되는 건 **visor 자체가 hang/이상** 인 안전망 시그널. |

> **주의** (LSN-D13958 운영 발견): `MainPID` 는 systemd dbus `org.freedesktop.systemd1.Service` 인터페이스에 있어서 `GetUnitTypePropertiesContext(unit, "Service")` 로 따로 조회해야 함. 그렇지 않으면 항상 0 반환.

#### `vpub_service_restart_total`

| 항목 | 값 |
|---|---|
| **타입** | Counter |
| **무엇** | systemd 가 publisher 를 재시작한 누적 횟수 |
| **어떻게** | dbus `NRestarts` (Service interface) |
| **주기** | 30초 |
| **코드** | `internal/collectors/service.go` |
| **운영 의미** | 갑자기 증가하면 publisher 가 crash 루프 — `journalctl -u validator-publisher` 로 원인 추적. |

#### `vpub_component_log_mtime_seconds{component=visor|bridge-voter|reference-oracle-publisher|outcome-voter}`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (unix sec) |
| **무엇** | 각 컴포넌트 로그 디렉토리의 **가장 최근 파일 mtime** (unix timestamp) |
| **어떻게** | `os.ReadDir(<dir>)` → 각 파일 `Stat()` → 가장 큰 ModTime |
| **주기** | 30초 |
| **코드** | [`internal/collectors/logmtime.go`](internal/collectors/logmtime.go), [`internal/logfs/logfs_os.go`](internal/logfs/logfs_os.go) |
| **컴포넌트별 path 매핑** | visor → `$VPUB_VISOR_LOG_DIR`. 나머지 3 → `$VPUB_COMPONENT_LOG_DIR/<component>/` |
| **운영 의미** | PromQL `time() - <metric>` 으로 staleness 계산. 컴포넌트가 hang 되면 mtime 갱신이 멈춤. 단 **visor 자체는 spawn manager 라 자체 로그 빈도 매우 낮음** — 알람에서 제외. |

### B. Tier 1 — Bridge / Oracle / Outcome / Slack

#### `vpub_bridge_rpc_up{name="alchemy|quicknode|..."}` / `vpub_bridge_rpc_latency_seconds{name}` / `vpub_bridge_rpc_check_total{name, status="ok|fail|timeout|auth_error"}`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (0/1) / Histogram (sec) / Counter |
| **무엇** | 각 Arbitrum RPC 의 헬스, latency, 누적 결과 |
| **어떻게** | 각 RPC URL 에 `eth_blockNumber` JSON-RPC 호출 (timeout 5s). 응답 200 + `result` 필드 → up=1, latency 측정. HTTP 401 또는 `Must be authenticated!` → `status="auth_error"`. |
| **주기** | 30초 |
| **코드** | [`internal/collectors/bridge_rpc.go`](internal/collectors/bridge_rpc.go), [`internal/rpc/rpc_http.go`](internal/rpc/rpc_http.go) |
| **운영 의미** | 메인넷은 7 provider 합의가 vote 의 전제 — 일부 down 이어도 voter 는 계속 동작하지만 quorum 위태 시 vote 실패. `auth_error` 가 누적 = API 키 만료/오류. |

#### `vpub_bridge_state_last_scanned_block` / `vpub_bridge_state_mtime_unix`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (int) / Gauge (unix sec) |
| **무엇** | bridge-voter 가 Arbitrum 을 어디까지 스캔했는지 + state.json 갱신 시각 |
| **어떻게** | `$VPUB_BRIDGE_STATE_PATH` (예: `bridge-voter-testnet-state.json`) 의 JSON `{"last_scanned_block": N, ...}` 파싱 + 파일 stat mtime |
| **주기** | 30초 |
| **코드** | [`internal/collectors/bridge_state.go`](internal/collectors/bridge_state.go) |
| **운영 의미** | **로그 mtime 보다 강한 진행성 시그널.** bridge-voter 가 hang/idle 이면 이 값이 갱신 안 됨. PromQL `delta(...[5m]) == 0` 으로 "스캔 정지" 직접 감지. |

#### `vpub_bridge_vote_total{status="ok|fail"}` / `vpub_bridge_last_vote_success_unix` / `vpub_bridge_rpc_disagreement_total`

| 항목 | 값 |
|---|---|
| **타입** | Counter / Gauge (unix sec) / Counter |
| **무엇** | bridge vote 제출 결과, 마지막 성공 vote 시각, RPC disagreement 이벤트 |
| **어떻게** | bridge-voter 로그 tail + 정규식 매칭. **vote fail** 은 `CRIT validator_publisher::bridge_voter::runner: critical error vote failed for deposit ...` (개별 이벤트). **vote ok** 시각은 `scanned ...` 라인 매칭 시 갱신. **disagreement** (임시 패턴) = `WARN ... RPC failed` (메인넷 가동 후 진짜 disagreement 라인 관찰되면 재정). |
| **주기** | 로그 tail (real-time) |
| **코드** | [`internal/collectors/vote_logs.go`](internal/collectors/vote_logs.go), [`internal/logtail/logtail_poll.go`](internal/logtail/logtail_poll.go) |
| **운영 의미** | scanned 라인의 `votes_failed=N` 은 cumulative gauge 라 counter 변환 부적합 → CRIT 라인 직접 카운트. testnet 입금 트래픽 0 이라 `last_vote_success_unix` 가 exporter start time 그대로 — 알람 룰에서 testnet 제외. |

#### `vpub_oracle_vote_total{status="ok|fail"}` / `vpub_oracle_last_vote_success_unix`

| 항목 | 값 |
|---|---|
| **타입** | Counter / Gauge (unix sec) |
| **무엇** | reference oracle vote 결과 |
| **어떻게** | reference-oracle-publisher 로그 tail. **ok** = `INFO validator_publisher::reference_oracle_publisher: oracle action sent`. **fail** = `validator_publisher::hyperliquid::exchange_client: hyperliquid response status=[45]xx`. |
| **주기** | 로그 tail |
| **코드** | `internal/collectors/vote_logs.go` |
| **운영 의미** | testnet 관찰 기준 평균 **4.3초** 간격으로 ok 발생 (max 102s). 알람 임계는 300s (5분) → 약 70 사이클 미스. |

#### `vpub_outcome_log_warn_total` / `vpub_outcome_log_crit_total`

| 항목 | 값 |
|---|---|
| **타입** | Counter |
| **무엇** | outcome-voter 모듈 한정 WARN/CRIT 라인 수 |
| **어떻게** | outcome-voter 로그 tail + 패턴 `\s+WARN\s+validator_publisher::outcome_voter` / `\s+(CRIT\|ERROR)\s+validator_publisher::outcome_voter`. **outcome-voter 모듈 path 한정** — oracle 의 price-drift WARN 폭주 제외. |
| **주기** | 로그 tail |
| **코드** | [`internal/collectors/outcome_logs.go`](internal/collectors/outcome_logs.go) |
| **운영 의미** | publisher 가 보내는 Slack 알람의 백업 카운터. testnet 5/22 일 502 일시 사건 10건 정확히 잡았던 사례 있음. |

#### `vpub_outcome_slack_msg_24h`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (int) |
| **무엇** | `outcome_actions_channel` 의 최근 24시간 메시지 수 |
| **어떻게** | Slack `conversations.history` (oldest = `now - 24h`) → 메시지 배열 길이 |
| **주기** | 5분 (Slack rate limit 보호) |
| **코드** | [`internal/collectors/outcome_slack.go`](internal/collectors/outcome_slack.go), [`internal/slackapi/slackapi_http.go`](internal/slackapi/slackapi_http.go) |
| **운영 의미** | 검토 적체 fallback. > 5건 누적 = 사람이 압박받음. |

#### `vpub_slack_api_ok`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (0/1) |
| **무엇** | Slack bot token 유효성 |
| **어떻게** | Slack `auth.test` API 호출 → 200 + `ok=true` → 1 |
| **주기** | 60초 |
| **코드** | [`internal/collectors/slack_health.go`](internal/collectors/slack_health.go) |
| **운영 의미** | **0 이면 publisher 가 보내는 모든 슬랙 알람이 누락 중일 수 있음** — 가장 무서운 silent failure. |

### C. Tier 2 — 업그레이드 트래킹

#### `vpub_binary_local_mtime_unix` / `vpub_binary_remote_mtime_unix` / `vpub_binary_remote_check_ok`

| 항목 | 값 |
|---|---|
| **타입** | Gauge (unix sec) × 2 / Gauge (0/1) |
| **무엇** | 로컬 publisher 바이너리 mtime, HF 가 게시한 remote 바이너리 `Last-Modified`, HEAD 호출 성공 여부 |
| **어떻게** | `os.Stat($VPUB_BINARY_PATH).ModTime()` + `http.Head($VPUB_BINARY_URL)` `Last-Modified` 헤더 파싱 (timeout 10s) |
| **주기** | 60초 (local) / 10분 (remote) |
| **코드** | [`internal/collectors/binary.go`](internal/collectors/binary.go), [`internal/binary/binary_http.go`](internal/binary/binary_http.go) |
| **운영 의미** | remote > local + 1h = HF 신규 announce → 변경사항 검토 후 **수동** 업그레이드. |

### D. Exporter self

`promhttp.Handler` 기본 `go_*` / `process_*` 외에:

- `vpub_exporter_collection_duration_seconds{collector=...}` (Histogram) — 각 collector tick 시간
- `vpub_exporter_collection_errors_total{collector, kind="timeout|api|parse|io"}` (Counter) — 외부 호출/파싱 에러

---

## Alert Rules — 언제 어떤 알람이 울리고, 울리면 뭘 봐야 하나

monitoring 레포의 `config/rules/hyperliquid_vpub_rule.yaml` 로 배포되는 22 룰. 자세한 yaml 은 [`monitoring/rules/hyperliquid_vpub_rule.yaml`](monitoring/rules/hyperliquid_vpub_rule.yaml), 명세는 [`specs/001-vpub-exporter/contracts/alerts.md`](specs/001-vpub-exporter/contracts/alerts.md).

### alertLevel 라우팅 (B-Harvest alertmanager 컨벤션)

| `alertLevel` | 채널 |
|---|---|
| `critical` | PagerDuty + `#ddoa-critical` |
| `high` | `#ddoa-high` |
| `medium`, `low` | `#ddoa-low` |

### Testnet vs Mainnet 분기

`critical` 6 룰은 모두 **mainnet 한정** (expr 에 `network!="testnet"` matcher). 동일 expr 의 testnet 복제 (`<Name>Testnet`) 가 `alertLevel: high` 로 따로 발화 — PagerDuty 노이즈 차단.

### A. Tier 0 — 프로세스 / 로그

#### `VpubServiceDown` (critical, mainnet) / `VpubServiceDownTestnet` (high)

- **expr**: `vpub_service_up{...,network!="testnet"} == 0` for 1m
- **trigger**: publisher 가 1분 이상 inactive
- **확인**:
  1. `ssh <publisher>; systemctl status validator-publisher`
  2. `journalctl -u validator-publisher --since "10 minutes ago" --no-pager`
  3. 의도된 정지인지, crash 인지 분기

#### `VpubChildMissing` (critical) / `VpubChildMissingTestnet` (high)

- **expr**: `vpub_child_count{...} < 3` for 30s
- **trigger**: child 가 3 미만이 30초 이상 — visor 가 재spawn 도 못하는 상황
- **확인**:
  1. `VISOR_PID=$(systemctl show -p MainPID --value validator-publisher); ps --ppid $VISOR_PID -o pid,cmd`
  2. visor 가 실행 중인데 child 가 없으면 → visor 자체 점검 (`journalctl -u validator-publisher`)
  3. > publisher 의 child auto-restart 가 robust 해서 정상 운영 중엔 거의 발화 안 됨. 발화 = 비정상.

#### `VpubLogStale` (high, 5분) / `VpubLogStaleLong` (critical, 30분, mainnet only) / `VpubLogStaleLongTestnet` (high, 30분, testnet)

- **expr**: `(time() - vpub_component_log_mtime_seconds{...,component!="visor"}) > 300/1800`
- **trigger**: bridge / oracle / outcome 컴포넌트 중 하나가 5분 (또는 30분) 이상 로그 갱신 X
- **확인**:
  1. publisher 머신에서 직접: `ls -la /tmp/validator-publisher/<component>/$(date -u +%Y%m%d)`
  2. mtime 이 정말 stale 이면 그 컴포넌트 hang
  3. `journalctl -u validator-publisher | grep -i <component>` — child crash 흔적
- **참고**: `component="visor"` 는 자체 로그 빈도 낮아 제외.

### B. Tier 1 — Bridge / Oracle / Outcome / Slack

#### `VpubBridgeRpcMajorityDown` (high)

- **expr**: `sum by (instance) (vpub_bridge_rpc_up{...}) < 4` for 5m
- **trigger**: 메인넷 7 RPC 중 살아있는 수가 4 미만 (testnet 은 RPC 수가 다름 — testnet 운영 시 매니저 재검토)
- **확인**:
  1. `curl -s localhost:8002/metrics | grep vpub_bridge_rpc_up`
  2. 어떤 provider 가 down 인지 확인
  3. RPC dashboard / 사용량 한도 / 키 만료 점검

#### `VpubBridgeRpcSingleDown` (medium)

- **expr**: `vpub_bridge_rpc_up{...} == 0` for 10m
- **trigger**: 특정 RPC 한 개가 10분 이상 down (정상 운영 중에도 종종 발생 가능 — informational)
- **확인**: provider dashboard, 키 만료, 결제 상태

#### `VpubBridgeRpcAuthError` (high)

- **expr**: `increase(vpub_bridge_rpc_check_total{status="auth_error",...}[10m]) > 5`
- **trigger**: 10분 내 401 응답 5번 초과
- **확인**: 해당 provider 의 API 키 만료/오류. 즉시 새 키 발급 + `/etc/vpub-exporter.env` 갱신 + `sudo systemctl restart vpub-exporter`

#### `VpubBridgeRpcDisagreement` (high)

- **expr**: `increase(vpub_bridge_rpc_disagreement_total[15m]) > 5` for 2m
- **trigger**: 15분 내 RPC 합의 실패 (임시 패턴 = `RPC failed` WARN, 메인넷 후 재정)
- **확인**: 특정 RPC 가 fork 또는 잘못된 응답 — 해당 provider 격리

#### `VpubBridgeStaleVote` (high, mainnet only) / `VpubBridgeStaleVoteLong` (critical, mainnet only) / `VpubBridgeStaleVoteLongTestnet` (high)

- **expr**: `(time() - vpub_bridge_last_vote_success_unix{...,network!="testnet"}) > 3600/21600`
- **trigger**: 1h / 6h 이상 성공 bridge vote 없음
- **확인**:
  1. 입금 트래픽 자체가 없을 수 있음 (낮은 시간대) — Arbitrum block explorer 에서 bridge contract 확인
  2. vote 시도는 있는데 fail 인지 → `vpub_bridge_vote_total{status="fail"}` rate 확인
  3. > **testnet 은 제외** — testnet 은 입금 트래픽 0건이라 자연 발화. mainnet only.

#### `VpubBridgeAllFail` (high)

- **expr**: 최근 1h 동안 ok vote 0건 + fail vote > 0
- **trigger**: vote 시도는 있는데 모두 실패
- **확인**:
  1. agent key 잔액 / nonce 상태
  2. `journalctl -u validator-publisher | grep -i "vote failed"` — 정확한 에러 코드 (예: code=514)
  3. publisher config.json 의 agent key 가 validator 에 제대로 등록됐는지

#### `VpubBridgeStateStuck` (high)

- **expr**: `delta(vpub_bridge_state_last_scanned_block[5m]) == 0` for 2m
- **trigger**: bridge-voter state.json 의 last_scanned_block 이 5분 이상 안 움직임 = Arbitrum 스캔 정지
- **확인**:
  1. bridge-voter 가 살아있는지 (`ps --ppid <visor_pid>`)
  2. Arbitrum RPC 가 모두 down 인지
  3. publisher 로그에서 bridge-voter 에러 패턴

#### `VpubOracleStaleVote` (high, 5분) / `VpubOracleStaleVoteLong` (critical, 30분, mainnet only) / `VpubOracleStaleVoteLongTestnet` (high)

- **expr**: `(time() - vpub_oracle_last_vote_success_unix{...}) > 300/1800`
- **trigger**: oracle action sent 가 5분 (또는 30분) 이상 없음 (정상 평균 4.3초)
- **확인**:
  1. reference-oracle-publisher 가 살아있는지
  2. Hyperliquid API 응답 상태 (`hyperliquid response status=...`)
  3. 가격 source (BinSpot, BybitSpot, ...) websocket 연결 상태 — `journalctl ... | grep -i "ws read error"`

#### `VpubOutcomePendingLong` (medium)

- **expr**: `vpub_outcome_slack_msg_24h > 5` for 30m
- **trigger**: outcome_actions 채널에 24h 내 5건 초과 메시지 30분 이상 유지
- **확인**:
  1. Slack 채널 들어가서 미검토 항목 확인
  2. Settle / Deploy 액션 검토 (publisher README 의 SOP — slashing 위험 주의!)

#### `VpubSlackTokenInvalid` (critical, mainnet) / `VpubSlackTokenInvalidTestnet` (high)

- **expr**: `vpub_slack_api_ok{...} == 0` for 5m
- **trigger**: Slack `auth.test` 가 5분간 fail
- **확인**:
  1. token 만료 / revoke / scope 변경
  2. Slack workspace admin 페이지에서 bot 상태 확인
  3. 새 토큰 발급 → `/etc/vpub-exporter.env` 갱신 → `sudo systemctl restart vpub-exporter`
  4. > **이거 0 이면 publisher 자체 슬랙 알람도 다 누락**. 가장 위험.

### C. Tier 2 — 업그레이드

#### `VpubBinaryUpdateAvailable` (medium)

- **expr**: `vpub_binary_remote_mtime_unix - vpub_binary_local_mtime_unix > 3600` for 30m
- **trigger**: remote (HF announce) 가 local 보다 1h 이상 신규
- **확인**:
  1. HF Telegram / Discord 의 announce 확인 (changelog)
  2. 변경사항 평가 후 수동 업그레이드 SOP 진행

#### `VpubBinaryRemoteCheckFail` (low)

- **expr**: `vpub_binary_remote_check_ok == 0` for 1h
- **trigger**: 1h 동안 binary URL HEAD 실패
- **확인**: URL 변경됐는지 확인 (HF 가 path 옮겼을 수 있음) → env 갱신

---

## Architecture

```
publisher 머신 (LSN-D13958 등)
┌─────────────────────────────────────────────────────────────┐
│  validator-publisher.service (systemd, User=admin/ubuntu)   │
│  └── visor                                                  │
│      ├── bridge-voter        → /tmp/validator-publisher/    │
│      │                          bridge-voter/YYYYMMDD       │
│      ├── reference-oracle-publisher                         │
│      └── outcome-voter                                      │
│                                                             │
│  vpub-exporter.service (systemd, same user)                 │
│  ├── /proc/* PPID scan      ← child_count                   │
│  ├── dbus GetUnit*Props     ← service_up, restart_total     │
│  ├── stat() log dirs        ← component_log_mtime           │
│  ├── tail log files         ← vote_total, log_warn_total    │
│  ├── stat() state.json      ← bridge_state                  │
│  ├── eth_blockNumber RPC    ← bridge_rpc_up, latency        │
│  ├── Slack auth.test        ← slack_api_ok                  │
│  ├── Slack conv.history     ← outcome_slack_msg_24h         │
│  └── HTTP HEAD binary URL   ← binary_remote_mtime           │
│                                                             │
│       │ :8002/metrics                                       │
└───────┼─────────────────────────────────────────────────────┘
        │
        ▼
   Prometheus (B-Harvest monitor.bharvest.io)
        │ (alert rules from monitoring repo)
        ▼
   alertmanager → PagerDuty + Slack (#ddoa-critical/high/low)
```

### Read-only 보존

- `os.Open` / `os.Stat` / `os.ReadDir` 만 사용 (write API X)
- systemd: `GetUnitPropertiesContext` / `GetUnitTypePropertiesContext` 만 (`StartUnit`/`StopUnit` 사용 안 함)
- Slack: `auth.test` / `conversations.history` (read API 만)
- systemd unit `ProtectSystem=full` + `ReadOnlyPaths=/home/admin/v-publisher` 추가 방어

---

## Build (개발자)

```bash
make build              # local (host arch)
make build-linux        # Linux amd64 (publisher 배포용)
make test               # unit + integration tests (race detector 포함)
make vet                # go vet
make promtool-check     # alert rule yaml validation (parser-wrap 시뮬)
make verify             # vet + test + promtool-check
make clean
```

### Project layout

```
vpub-exporter/
├── cmd/vpub-exporter/main.go
├── internal/
│   ├── config/         # env / flag parsing
│   ├── systemd/        # dbus probe (Service interface)
│   ├── procs/          # /proc/*/stat PPID scan
│   ├── logfs/          # log dir latest-file stat
│   ├── logtail/        # log file tail + pattern matching
│   ├── rpc/            # Arbitrum RPC client (eth_blockNumber)
│   ├── slackapi/       # Slack auth.test, conversations.history
│   ├── binary/         # HTTP HEAD Last-Modified
│   └── collectors/     # Prometheus collectors (1 per metric group)
├── monitoring/
│   ├── agents/         # staging for monitoring repo (instance TOML)
│   └── rules/          # staging for monitoring repo (alert rule yaml)
├── systemd/
│   └── vpub-exporter.service
├── env/
│   └── vpub-exporter.env.example
├── tests/integration/
└── specs/001-vpub-exporter/   # spec-kit: spec / plan / contracts / quickstart / tasks
```

---

## 운영 검증 결과 (LSN-D13958 testnet, 2026-05-23)

Tier 0 MVP 가동 검증:

| Success Criterion | 결과 | 비고 |
|---|---|---|
| **SC-001** publisher stop → critical 알람 < 90s | ✅ 합격 | `vpub:service:down:testnet` (high) 정확히 발화, resolve 도 OK |
| **SC-002** child kill detect (재정의) | ✅ 합격 | publisher 가 child kill 후 1-3초 즉시 재spawn → `VpubChildMissing` 발화 0건 = 안전망 룰 정상 |
| **SC-003** `/metrics` p95 < 200ms | ✅ 합격 | 실측 ~4ms (목표의 2%) |
| **SC-004** RSS < 100MB, CPU < 5% | ✅ 합격 | 실측 RSS 8.7MB |
| **SC-005** Read-only 보존 | ✅ 합격 | publisher 파일 변경 0 (systemd `ProtectSystem=full` + `ReadOnlyPaths`) |
| **SC-006** 1개월 down 감지율 100% | ⏳ 운영 누적 중 | |
| **SC-007** 1주일 false-positive < 10% | ⏳ 운영 누적 중 | testnet 자연 false-positive 룰들 이미 mainnet only 로 분기 완료 |
| **SC-008** Tier 0 PR → 배포 < 24h | ✅ 합격 | 같은 날 가동 완료 |

운영 발견 사항 (모두 백포트 완료):

1. **systemd dbus** `MainPID`/`NRestarts` 는 `org.freedesktop.systemd1.Service` interface 에서만 조회 가능. `Unit` interface 로는 None 반환 — 원래 `vpub_child_count` 가 항상 0 이었던 원인. `GetUnitTypePropertiesContext("Service")` 분리 호출로 정정.
2. **systemd unit `PrivateTmp=yes`** 가 publisher 의 `/tmp/validator-publisher/` 컴포넌트 로그 격리. **반드시 `PrivateTmp=no`** (publisher 의 `v-publisher.service.full` 도 동일 의도 명시).
3. **publisher visor 의 robust spawn** — child kill 후 1-3초 즉시 재spawn. `VpubChildMissing` 30s 임계로 detect 불가 → "안전망 룰" 로 재정의.
4. **`VpubLogStale`/`Long` 에 `component!="visor"` 매처** — visor 자체 로그 빈도 매우 낮아 false-positive.
5. **`VpubBridgeStaleVote` mainnet only** — testnet 입금 트래픽 0건이라 자연 영구 발화.
6. **critical 6 룰 mainnet 한정 + `<Name>Testnet` (high) 복제** — testnet 에서 PagerDuty 노이즈 차단.

전체 검증 보고서: [`specs/001-vpub-exporter/checklists/requirements.md`](specs/001-vpub-exporter/checklists/requirements.md). 운영 발견 R-013~018 상세: [`specs/001-vpub-exporter/research.md`](specs/001-vpub-exporter/research.md).

---

## Constitution & Safety

[`.specify/memory/constitution.md`](.specify/memory/constitution.md) 의 7 원칙 (NON-NEGOTIABLE 표기) 을 모든 PR 이 준수해야 한다:

1. **Outside-the-Box Monitoring** — publisher 의 self-alarm 에 의존 X
2. **No Side Effects on Publisher** — read-only, 자동 vote/restart/unjail 0
3. **Monitoring 레포 Convention 준수** — `vpub_` prefix, `alertLevel` 5종, promtool check gate
4. **Secrets Never in Code / Metrics / Labels** — env-only, config.json 직접 파싱 금지
5. **Non-Blocking Scrape** — `/metrics` 즉시 응답, 외부 호출은 goroutine + cache
6. **Time-Sensitive Truth from Logs** — 로그 패턴은 env override 가능, 가동 첫날 회귀 필수
7. **Tier Gating** — Tier 0 안정 후 Tier 1 출시

매 PR 의 Constitution Gate 3 grep:

```bash
grep -rE "xoxb-|0x[a-fA-F0-9]{40}" --include="*.go" --include="*.yaml" .   # 시크릿 노출 = 0
grep -rE "exec\.Command.*systemctl|StartUnit|StopUnit|kill\(" --include="*.go" .  # 자동 액션 = 0
grep -hoE 'alertLevel: "[a-z]+"' monitoring/rules/*.yaml | sort -u  # critical/high/medium/low/disk 안에서만
```

---

## Links

- **Specs**: [`specs/001-vpub-exporter/`](specs/001-vpub-exporter/)
- **Contracts**: [metrics](specs/001-vpub-exporter/contracts/metrics.md) / [alerts](specs/001-vpub-exporter/contracts/alerts.md)
- **Quickstart (검증 시나리오)**: [`specs/001-vpub-exporter/quickstart.md`](specs/001-vpub-exporter/quickstart.md)
- **Research notes (R-001~012)**: [`specs/001-vpub-exporter/research.md`](specs/001-vpub-exporter/research.md)
- **hl-validator-publisher**: https://binaries.hyperliquid-testnet.xyz/validator-publisher/
- **Hyperliquid docs**: https://hyperliquid.gitbook.io/hyperliquid-docs

---

## License

Internal B-Harvest tool. Not for external distribution without authorization.
