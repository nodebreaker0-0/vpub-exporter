# Phase 0 Research — vpub-exporter

본 문서는 spec.md 의 가정 / NEEDS CLARIFICATION 항목을 가동 첫날 (testnet) 에 실제로 확인하여 확정하기 위한 체크리스트다. 확정 결과는 본 문서를 갱신하고, 영향 받는 contracts/ 또는 plan.md 도 동기화한다.

## R-001 — 로그 디렉토리 실제 경로 ✅ 확정 (2026-05-23, testnet 9.7h)

**Gas pothesis 가 부분 틀렸음**.

**확정 결과**:
- visor 자체 로그: `~/v-publisher/log/YYYYMMDD` (service 의 `--log-dir log` 가 영향 — visor **자신만**)
- 3 child 컴포넌트 로그: **`/tmp/validator-publisher/{bridge-voter,reference-oracle-publisher,outcome-voter}/YYYYMMDD`** (visor default, `--log-dir` 영향 X)
- v-publisher.service.full 의 `PrivateTmp=false` 가 이 경로 의도. 정상.

**환경 차이**:
- testnet 머신 user = `admin`, WorkingDirectory = `/home/admin/v-publisher`
- mainnet 머신 user = `ubuntu`, WorkingDirectory = `/home/ubuntu/v-publisher`
- `/tmp/validator-publisher` 는 user 무관 — 양쪽 동일.

**영향 (백포트 완료)**:
- env.example: `VPUB_LOG_DIR` 한 변수 → **`VPUB_VISOR_LOG_DIR` + `VPUB_COMPONENT_LOG_DIR`** 둘로 분리
- config.go default: 동일하게 두 변수
- spec.md Assumptions §1 정정 필요

---

## R-002 — 컴포넌트별 로그 파일명 패턴 ✅ 확정

**확정 결과**: 모든 컴포넌트 디렉토리에서 **`YYYYMMDD`** 단일 파일 (확장자 없음). 하루 1 파일, 자정 UTC 회전.

**영향**: `logfs.LatestFile()` 의 단순 "최신 mtime" 로직으로 충분. 별도 glob 필터 불필요.

추가 발견 — `reference-oracle-publisher/<TOKEN>/YYYYMMDD` 가 따로 존재 (170 토큰, 각 가격 raw JSON 로그). **본 exporter 의 모니터링 대상 아님** (cardinality 폭증 위험 + 정보 가치 낮음).

---

## R-003 — Vote / Disagreement / Warn / Crit 로그 라인 패턴 ✅ 확정

**확정 결과** (9.7h testnet 로그):

### bridge-voter (`validator_publisher::bridge_voter::{rpc,runner}`)
- vote 카운터는 **라인 매칭이 아닌 숫자 추출**:
  ```
  2026-05-23T... INFO  validator_publisher::bridge_voter::runner: scanned from_block=N to_block=N
    candidates_seen=N tracked_candidates=N
    votes_sent=N votes_skipped=N votes_failed=N
    deposits=N advanced_to="N"
  ```
  Δ(votes_sent) → `vpub_bridge_vote_total{status="ok"}` 증가, Δ(votes_failed) → `{status="fail"}` 증가.
  9.7h testnet 동안 votes_sent=0, votes_failed=0 (입금 트래픽 0건 — 정상).

- RPC 헬스: `INFO  validator_publisher::bridge_voter::rpc: rpc {request|response} provider=X method="..." [status=200] ...`
- RPC 실패: `WARN  validator_publisher::bridge_voter::runner: RPC failed eth_getLogs provider="X" error=...`
  9.7h testnet 동안 15건, 전부 infura (`-32603: service temporarily unavailable`).
- **disagreement 별도 단어 없음** — testnet votes=0 라 합의 라인 미관찰. 메인넷 가동 후 재관찰 필요.

### reference-oracle-publisher (`validator_publisher::reference_oracle_publisher::{publisher,sources}`, `validator_publisher::hyperliquid::exchange_client`)
- vote ok: `INFO  validator_publisher::reference_oracle_publisher: oracle action sent`
  → 다음 라인: `INFO  validator_publisher::hyperliquid::exchange_client: hyperliquid response status=200 response={"status":"ok",...}`
  9.7h 동안 **8084건**, 평균 4.3초 간격, max 102초.
- vote fail (가설): `hyperliquid response status=[45]xx` 또는 `response={"status":"err"...}`. testnet 9.7h 동안 0건.
- WARN price drift (정상 동작): `WARN  validator_publisher::reference_oracle_publisher::publisher: non-trusted source price is (within|more than) 1% of trusted median source=X coin="Y" price=N median=N`
  → 대량 발생 (2647건/3000초). 알람 X. WARN 카운터에서 제외 권장 (`outcome` 전용으로 변경).
- ws 재연결 (정상): `WARN  validator_publisher::reference_oracle_publisher::sources: ws read error source="X"`

### outcome-voter (`validator_publisher::outcome_voter`)
- 정상: `INFO  validator_publisher::outcome_voter: spec check completed` (≈5초 주기, 9.7h 6181건)
- WARN: 0건 관찰
- CRIT: `CRIT  validator_publisher::outcome_voter: critical error failed to fetch outcome meta err=...` (9.7h 10건, 7:13~7:14 일시 502)

**영향 (백포트 완료)**:
- env.example 의 default 패턴 위 실제 문자열로 정정
- `VPUB_LOG_WARN_PATTERNS` / `VPUB_LOG_CRIT_PATTERNS` 는 **outcome-voter 모듈 한정** (oracle WARN 폭주 제외)
- `VPUB_VOTE_OK_PATTERNS` / `VPUB_VOTE_FAIL_PATTERNS` → bridge 의 votes_sent/votes_failed 정수 캡처 그룹 사용
- `VPUB_ORACLE_OK_PATTERNS` / `VPUB_ORACLE_FAIL_PATTERNS` 분리 (oracle 별도 — 코드 변경 검토)
- `VPUB_DISAGREEMENT_PATTERNS` → 임시로 "RPC failed" 매칭, 메인넷 후 재정정

---

## R-004 — Reference Oracle 게시 주기 ✅ 확정 (강력 조정 필요)

**확정 결과**: 평균 **4.3초**, max **102초** (testnet 9.7h, n=8084).

**알람 임계 재조정**:
- 현재 `VpubOracleStaleVote > 7200s` (2h) = **1675 사이클 미스** → **사실상 무용**
- 권장:
  - **>300s (5분, max=102s 의 3x 안전마진) → high**
  - **>1800s (30m) → critical**

**영향**: `monitoring/rules/hyperliquid_vpub_rule_tier1.yaml` 의 `VpubOracleStaleVote` 임계 정정 + 새 `VpubOracleStaleVoteLong` 추가.

---

## R-005 — 메인넷 RPC Quorum 정확값 ⏸️ 메인넷 가동 후 확정

**testnet 관찰**: votes_sent=0 (입금 트래픽 0건) — 합의 라인 미관찰.

**현재 임계**: `VpubBridgeRpcMajorityDown` `sum(vpub_bridge_rpc_up) < 4` (메인넷 7 → 4).

**testnet 한정 별도 룰 필요할 수 있음**: testnet 3 RPC → `< 2` 로 별도 룰. 또는 network 라벨로 분기.

**영향**: alerts.md 와 yaml 에 testnet/mainnet 룰 분리 필요. Phase N 시점에 처리.

---

## R-006 — 메인넷 Publisher 바이너리 URL

**질문**: HF 가 announce 할 메인넷 publisher 바이너리의 정확한 URL?

**Why it matters**: FR-013 (upgrade tracking) 의 데이터 소스. testnet 은 `https://binaries.hyperliquid-testnet.xyz/validator-publisher/visor` 확정.

**확정 방법**:
- HF announce 대기 (Discord / Telegram)
- 또는 `https://binaries.hyperliquid.xyz/validator-publisher/visor` 추정 후 가동 직전 확인

**확정 결과**: _(채움)_

**영향**: `VPUB_BINARY_URL` env, env.example.

---

## R-007 — Outcome Channel ID

**질문**: outcome_actions_channel 의 실제 Slack channel ID?

**Why it matters**: FR-010 (24h msg count) 의 호출 대상. 이름이 아닌 ID 필요 (channel 이름 변경에 강함).

**확정 방법**: Slack 워크스페이스 admin 페이지 또는 `conversations.list` 호출.

**확정 결과**: _(채움)_

**영향**: `VPUB_OUTCOME_CHANNEL` env (시크릿은 아니지만 env 로 주입).

---

## R-008 — Go Module Path ✅ 확정 (2026-05-23)

**질문**: B-Harvest 내부 Go 컨벤션상 module path 는?

**확정 결과**: **`github.com/bharvest/vpub-exporter`**

**근거**: 사용자 결정 — 기존 `hlmon` 등의 코드 베이스와 **분리**해서 새로 작성. 기존 도구 go.mod 참조 안 함. 코드 컨벤션 / 라이브러리 / 구조 모두 본 spec-kit 산출물 기준으로 독립적으로 구축.

**참고**: 위 module 경로의 prefix (`github.com/bharvest`) 가 실제 B-Harvest GitHub organization 이름과 다르면 코드 작성 시 멈추고 질문 필요.

---

## R-009 — Best practice: Prometheus client_golang 표준 사용 패턴

**Decision**: 표준 `prometheus.NewRegistry()` + collector 별 `prometheus.Collector` 인터페이스 구현 + `promhttp.HandlerFor(registry, promhttp.HandlerOpts{})`.

**Rationale**: 가장 흔한 패턴. 코드 양 적음. promauto 는 전역 registry 의존성이라 테스트 어려움 → 명시 registry.

**Alternatives considered**: `promauto` (전역 — 거부), OpenMetrics 직접 텍스트 출력 (오버킬).

---

## R-010 — Best practice: systemd 상태 조회

**Decision**: `github.com/coreos/go-systemd/v22/dbus` 의 `Conn` 으로 `GetUnitProperties("v-publisher.service")` 호출. `ActiveState`, `MainPID`, `NRestarts` 추출.

**Rationale**: dbus 직접 호출이 가장 가볍고 신뢰성 높음. `exec systemctl is-active` 도 가능하지만 매 5s exec 는 자원 낭비.

**Alternatives considered**: `os/exec systemctl ...` (거부 — 오버헤드), `journalctl --since` 파싱 (거부 — 복잡).

---

## R-011 — Best practice: 로그 파일 tail (회전 인식)

**Decision**: 자체 구현 (Go std `os.Open` + `io.Reader` 위에 line-by-line). 매 30초마다 "현재 watched file == 최신 파일인지" 체크, 다르면 재오픈.

**Rationale**: `hpcloud/tail` 같은 라이브러리는 inotify 기반인데, publisher 로그는 일별 rotation (`YYYYMMDD` 새 파일) 이라 inotify 만으로 부족. 직접 polling + rename 감지가 더 robust.

**Alternatives considered**: `hpcloud/tail` (회전 인식 약함), `fsnotify` 직접 사용 (구현 복잡).

---

## R-012 — Best practice: Slack API client

**Decision**: 표준 `net/http` + JSON 직접 호출. `auth.test`, `conversations.history` 2개만 필요 → 라이브러리 의존 불필요.

**Rationale**: 의존성 최소화 (Constitution III + 기존 hl-exporter 컨벤션). 인증은 `Authorization: Bearer <token>` 헤더.

**Alternatives considered**: `slack-go/slack` (거부 — 오버킬).

---

## 완료 체크

- [x] R-001 ✅ 확정 (2026-05-23, testnet 9.7h) — visor + component log dirs 분리
- [x] R-002 ✅ 확정 — `YYYYMMDD` 단일 파일
- [x] R-003 ✅ 확정 — 실패턴 추출, env.example default 갱신
- [x] R-004 ✅ 확정 — 평균 4.3s, 임계 강력 조정
- [ ] R-005 ⏸️ 메인넷 가동 후 (testnet votes=0). 임시 가설 `quorum=4`.
- [ ] R-006 ⏸️ HF 메인넷 binary URL announce 대기. 추정 `https://binaries.hyperliquid.xyz/validator-publisher/visor`.
- [x] R-007 ✅ 사용자 testnet 가동 시 channel ID 직접 주입 완료 (Slack 메시지 수신 확인)
- [x] R-008 ✅ 확정 (독립 module path `github.com/bharvest/vpub-exporter`)
- [x] R-009 ~ R-012 ✅ best practice 결정 완료
- [x] **추가 발견 (R-013, 2026-05-23 운영)** — systemd dbus `MainPID`/`NRestarts` 는 `Service` interface 분리 호출 필수
- [x] **추가 발견 (R-014)** — systemd unit `PrivateTmp=no` 필수 (publisher `/tmp/...` 격리 차단 X)
- [x] **추가 발견 (R-015)** — publisher visor 가 child kill 후 1-3초 즉시 재spawn → `VpubChildMissing` 안전망 룰로 재정의 (SC-002 보정)
- [x] **추가 발견 (R-016)** — `VpubLogStale` 에 `component!="visor"` 매처 (visor 자체 로그 빈도 낮음 — false-positive 차단)
- [x] **추가 발견 (R-017)** — `VpubBridgeStaleVote` mainnet only (testnet 입금 0건 자연 발화 차단)
- [x] **추가 발견 (R-018)** — critical 6 룰 mainnet 한정 + testnet `<Name>Testnet` (high) 복제 (PagerDuty noise 차단)
- [x] **추가 발견 (R-019, 2026-05-24)** — visor 가 child binary 3개를 자체 `/<child>/active` polling 으로 자동 동기화. visor 만 추적해선 child update/실패 불검출.
- [x] **추가 발견 (R-019b, 2026-05-24)** — mainnet 환경 차이 3건 식별 (사용자 보고): ubuntu homedir / 7 RPC / 로그량 ~10× testnet. 코드 변경 없이 systemd drop-in + env.mainnet.example + R-005 실측 절차 + 부하 측정 스크립트로 흡수.
- [x] **추가 발견 (R-017b, 2026-05-24)** — testnet `VpubBridgeStaleVoteLongTestnet` 임계 6h → 7d (`> 604800`). testnet 입금 트래픽 0건 영구 false-positive 차단. mainnet `VpubBridgeStaleVoteLong` (critical) 은 6h 그대로 — mainnet 가동 후 vote 빈도 실측 후 R-017c 로 재조정 예정.
- [ ] **R-017c PENDING** — mainnet 가동 후 24h~7d 동안 `vpub_bridge_last_vote_success_unix` 의 갱신 빈도 (inter-vote interval) 분포 측정. p99 의 2~3× 값을 `VpubBridgeStaleVoteLong` 임계로 확정. 측정 명령: `bash scripts/mainnet_burst_check.sh` 결과 + Prometheus query `histogram_quantile(0.99, rate(...))`.
- [x] **R-020 (2026-05-24)** — HF announce: mainnet validator-publisher live. bridge_voter / reference_oracle_publisher 는 **다음 L1 upgrade 까지 disabled**. outcome_voter 만 가동. 영향: bridge/oracle 의존 5 룰 임계 일시 30d 로 silence + alertLevel 정책 변경 (testnet 7룰 high→low, mainnet critical 3룰→high). 복원 절차 본 문서 R-020 섹션 참조.

---

## R-020 — mainnet 일시 silence + alertLevel 정책 변경 (HF L1 upgrade 대기)

**Trigger**: HF Telegram announce (2026-05-24) — "The mainnet validator publisher is live. The bridge voter and reference oracle publisher are currently disabled pending the next L1 network upgrade. The outcome deploy news feed should be operational on mainnet."

**Decision A — 5 룰 임계 일시 silence**:

| 룰 | 원래 임계 | 새 임계 (30d) | 의미 |
|---|---|---|---|
| VpubBridgeStaleVote | `> 3600` (1h) | `> 2592000` | bridge_voter disabled — `last_vote_success_unix` 영원히 stale |
| VpubBridgeStaleVoteLong | `> 21600` (6h) | `> 2592000` | 동상 + alertLevel critical→high |
| VpubBridgeStateStuck | `delta(...[5m]) == 0` | `delta(...[30d]) == 0` | bridge state.json mtime 영구 정체 |
| VpubOracleStaleVote | `> 300` (5m) | `> 2592000` | reference_oracle disabled — `last_vote_success_unix` 영원히 stale |
| VpubOracleStaleVoteLong | `> 1800` (30m) | `> 2592000` | 동상 + alertLevel critical→high |

VpubBridgeAllFail (vote 시도 자체 없음 → counter 0 → 자동 no-fire) 와 VpubBridgeRpcMajorityDown (vpub-exporter 의 자체 RPC ping 은 publisher 와 무관 — 정상 동작) 은 변경 X.

**Decision B — alertLevel 정책 변경**:

mainnet critical 유지 (silent failure 3개만):
- `VpubServiceDown` — publisher 자체 다운
- `VpubChildMissing` — spawn manager 망가짐
- `VpubSlackTokenInvalid` — publisher 의 모든 슬랙 알람 누락 (가장 무서움)

mainnet critical → high (3개):
- `VpubLogStaleLong`
- `VpubBridgeStaleVoteLong` (이미 R-020 에서 silence 중이지만 어쨌든)
- `VpubOracleStaleVoteLong`

testnet 전부 high → low (7개):
- `VpubServiceDownTestnet` / `VpubChildMissingTestnet` / `VpubLogStaleLongTestnet`
- `VpubBridgeRpcMajorityDownTestnet` / `VpubBridgeStaleVoteLongTestnet`
- `VpubOracleStaleVoteLongTestnet` / `VpubSlackTokenInvalidTestnet`

**Rationale**:
- testnet 알람이 ddoa-high 까지 가서 운영자 주의력 분산 — testnet 은 ddoa-low 만 충분
- mainnet critical = PagerDuty 호출 = 실제 운영자 콜아웃. silent failure 3개 외에는 PagerDuty 가치 부족 (high 채널 + 운영자 자체 모니터링이면 충분)

**복원 절차 (L1 upgrade 후 bridge_voter / oracle 재가동 시)**:

```bash
cd /Users/ijeseon/hl-agent/validator/vpub-exporter

# 1) 임계 복원 (5 룰)
python3 <<'PY'
import yaml, re
f = "monitoring/rules/hyperliquid_vpub_rule_tier1.yaml"
restore = {
    "VpubBridgeStaleVote":       ("expr", lambda e: re.sub(r"> 2592000\b", "> 3600", e)),
    "VpubBridgeStaleVoteLong":   ("expr", lambda e: re.sub(r"> 2592000\b", "> 21600", e)),
    "VpubOracleStaleVote":       ("expr", lambda e: re.sub(r"> 2592000\b", "> 300", e)),
    "VpubOracleStaleVoteLong":   ("expr", lambda e: re.sub(r"> 2592000\b", "> 1800", e)),
    "VpubBridgeStateStuck":      ("expr", lambda e: e.replace("[30d])", "[5m])")),
}
doc = yaml.safe_load(open(f))
for r in doc["rules"]:
    if r["alert"] in restore:
        k, fn = restore[r["alert"]]
        r[k] = fn(r[k])
# alertLevel 정책은 사용자 합의 — 자동 복원 안 함. 필요 시 수동.
class D(yaml.SafeDumper):
    def increase_indent(self, flow=False, indentless=False): return super().increase_indent(flow, False)
def s(d, x): return d.represent_scalar("tag:yaml.org,2002:str", x, style="|") if "\n" in x else d.represent_scalar("tag:yaml.org,2002:str", x)
D.add_representer(str, s)
open(f, "w").write(open(f).readlines()[0:17].__iter__().__next__() if False else open(f).read().split("rules:")[0] + "rules:\n" + yaml.dump(doc, Dumper=D, sort_keys=False, indent=2, allow_unicode=True, width=2000).split("rules:\n",1)[1])
PY

# 2) 통합본 재생성 + monitoring sync
# (위에서 본 동일한 python 머지 스크립트)

# 3) make verify + git push
make verify
```

**Alternatives considered**:
- 룰을 `disable_alarm='true'` 라벨로 silencing → 우리 metric 라벨 인프라가 이를 지원하려면 별도 코드 변경 필요. 일시 silence 엔 임계 변경이 더 간단.
- 영향 룰 yaml 통째로 주석 처리 → 까먹기 쉬움. 임계 변경이 grep 으로 찾기 좋음.

---

---

## R-019 — child binary 자동 동기화 모델 (2026-05-24 운영 로그 분석)

**발견**: `/home/admin/v-publisher/log/YYYYMMDD` 의 visor 로그 분석 결과, visor 는 다음 3 child 를 HF announce URL 의 별도 path 에서 **자체 polling 으로 download** 한다:

- `https://binaries.hyperliquid-testnet.xyz/validator-publisher/bridge-voter/active`
- `https://binaries.hyperliquid-testnet.xyz/validator-publisher/outcome-voter/active`
- `https://binaries.hyperliquid-testnet.xyz/validator-publisher/reference-oracle-publisher/active`

`/active` 응답 본문 = 현재 active height (예: `5`), `Last-Modified` 헤더는 active alias 의 마지막 갱신 시각. visor 는 이 height 가 증가하면 `/<child>/<height>` URL 로 binary 를 download 한 뒤 spawn 교체.

```
2026-05-22T06:00:08 INFO visor: downloading new binary
  self.binary_name="outcome-voter"
  binary_url=".../validator-publisher/outcome-voter/1" height=1
2026-05-22T06:00:10 INFO visor: spawning new process
  binary_path="/home/admin/v-publisher/outcome-voter" height=1
```

**핵심 비대칭**:
- **visor**: HF 가 `/validator-publisher/visor` 에 publish → **사람이 wget/install 해야 적용**. 알람 발화 = "운영자가 액션할 시간".
- **child × 3**: HF 가 `/<child>/active` 갱신 → **visor 가 자동 download + spawn 교체**. 알람 발화 = "visor 의 maybe_download 가 망가졌으니 운영자가 visor 보러갈 시간".

**결정**: binary tracking 을 2-tier 로:
1. visor (manual upgrade): 기존 `remote_mtime - local_mtime > 60` expr 유지, `component="visor"` 라벨로 식별.
2. child × 3 (auto-sync fail): **visor 로그의 "downloading new binary" 라인 시각** (`vpub_binary_download_started_unix{component=<child>}`) 과 child file mtime 비교. `download_started_unix - local_mtime > 60` 이 1분 유지 = download 실패. 정상 시 mtime 이 거의 즉시 갱신되어 expr 음수 → 자동 resolve.

**왜 HTTP HEAD 가 아닌 로그 기반인가**: child URL HEAD 가 가능하긴 하지만 (200 응답 확인됨) 이는 "HF 가 publish 했다" 만 알려주고 **visor 가 download 시도했는지/실패했는지** 의 시그널은 안 줌. 로그 기반은 "visor 가 시도했는데 mtime 안 따라옴" 을 직접 잡아 진짜 실패 모드 검출. 또한 HEAD 추가 호출 (+3 req/min) 없이 기존 logtail 인프라 재사용.

**Rationale**: child auto-sync 실패는 일반적으로 maybe_download 의 retry (3s 간격) 가 충분히 견딘다. 60s 동안 mtime 미갱신이면 retry budget 초과 = 실제 실패. testnet 가동 로그 분석상 정상 download 완료까지 평균 2~3초.

**Alternatives considered**:
- 옵션 B (per-child HEAD polling, 3× /active gauge): HEAD 호출 4배 + 메트릭 4 시리즈 추가. 동일 결론 더 비싸게 도달.
- 옵션 C (logtail count only): retry 가 정상 self-heal 도 발화 → noise. 채택 X.

**임계**: download_started 후 60s 안에 mtime 미갱신 + `for: 1m` 추가 안정. 총 timeout ~120s.

---

## R-019b — mainnet 환경 차이 흡수 (2026-05-24 사용자 보고)

**Decision**: 코드는 그대로, **배포 산출물 (systemd drop-in + env example + 부하 측정 가이드) 로 환경 차이 흡수**. 메인넷 가동 시점 코드 빌드/배포 없이 cp/install + systemctl daemon-reload 만으로 완료.

| 메인넷 차이 | 흡수 방식 | 산출물 |
|---|---|---|
| User: admin → ubuntu | systemd drop-in `mainnet.conf` 에 `User=ubuntu`, `ReadOnlyPaths=/home/ubuntu/v-publisher` | `systemd/mainnet.conf` |
| RPC: 3 → 7 | bridge_rpc collector 가 list 기반이라 자동 확장. quorum 임계 (`< 4`) 는 R-005 미확정 — 가동 첫 vote 시점 실측 | `quickstart.md QS-2.1m` |
| 로그량 ~10× | logtail offset 기반 incremental read 라 자동 흡수. MemoryMax 200M → 400M drop-in. collection_duration_p95 가 SC-003 (< 1s) 안에 들어가는지 1h/6h/24h 측정 | `scripts/mainnet_burst_check.sh`, MemoryMax in drop-in |
| critical 6 룰 첫 발화 | PagerDuty dry-run 절차로 testalrt 채널 우회 검증 | `quickstart.md QS-2.1p` |

**Rationale**: R-019 (코드 변경) 와 다르게 R-019b 는 모두 **환경 변수 / unit drop-in / 운영 절차** 로 해결 가능 — 코드를 mainnet/testnet 분기로 만들면 복잡성 증가 + Constitution III (의존성 최소화) 위반.

**Alternatives considered**:
- 코드에 `network` 분기 + auto-detect (homedir / RPC count) → 거부. unit override + env 가 정답.
- mainnet 전용 binary (build tag) → 거부. 단일 binary 가 환경 차이 모두 흡수해야 운영 단순.

---
