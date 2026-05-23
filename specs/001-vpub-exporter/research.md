# Phase 0 Research — vpub-exporter

본 문서는 spec.md 의 가정 / NEEDS CLARIFICATION 항목을 가동 첫날 (testnet) 에 실제로 확인하여 확정하기 위한 체크리스트다. 확정 결과는 본 문서를 갱신하고, 영향 받는 contracts/ 또는 plan.md 도 동기화한다.

## R-001 — 로그 디렉토리 실제 경로

**질문**: publisher 의 컴포넌트 로그가 실제로 어디에 떨어지는가? `--log-dir log` (사용자 service) → `/home/admin/v-publisher/log/` 인지, README default `/tmp/validator-publisher/` 인지?

**Why it matters**: FR-003 (log mtime) 의 데이터 소스 경로. 잘못 잡으면 모든 Tier 0 알람 무력화.

**확정 방법**:
```bash
# publisher 가동 후 10분 뒤
ls -la /home/admin/v-publisher/log/
ls -la /tmp/validator-publisher/ 2>/dev/null
sudo find / -path /proc -prune -o -name "*.log" -newer /tmp/start-mark -print 2>/dev/null | head -30
```

**확정 결과**: _(testnet 가동 후 채움)_

**영향**: `VPUB_LOG_DIR` 환경변수 default. config 패키지 + systemd unit + env.example.

---

## R-002 — 컴포넌트별 로그 파일명 패턴

**질문**: 각 컴포넌트의 로그 파일이 `YYYYMMDD` 만인지, `.log` 접미사가 있는지, 별도 prefix 가 있는지?

**Why it matters**: logfs 패키지의 "최신 파일 찾기" 로직.

**확정 방법**:
```bash
ls -la /home/admin/v-publisher/log/bridge-voter/
ls -la /home/admin/v-publisher/log/reference-oracle-publisher/
ls -la /home/admin/v-publisher/log/outcome-voter/
ls -la /home/admin/v-publisher/log/    # visor 자체
```

**확정 결과**: _(채움)_

**영향**: `logfs.LatestFile()` 의 glob 패턴.

---

## R-003 — Vote / Disagreement / Warn / Crit 로그 라인 패턴

**질문**: bridge / oracle 컴포넌트 로그에서 "vote submitted" / "vote failed" / "disagreement" / "warning" / "critical" 라인의 정확한 문자열은?

**Why it matters**: FR-006 / 008 / 009 의 패턴 매칭 정확도.

**확정 방법**:
```bash
# 1시간 가량 정상 가동 후
grep -iE "vote|submit|fail|disagree|mismatch|warn|error|crit" \
  /home/admin/v-publisher/log/bridge-voter/$(date -u +%Y%m%d) | head -50
grep -iE "vote|submit|fail|warn|error|crit" \
  /home/admin/v-publisher/log/reference-oracle-publisher/$(date -u +%Y%m%d) | head -50
grep -iE "warn|error|crit" \
  /home/admin/v-publisher/log/outcome-voter/$(date -u +%Y%m%d) | head -50
```

**확정 결과**: _(채움 — 패턴들을 정규식 형태로)_

**영향**: env default 값:
- `VPUB_VOTE_OK_PATTERNS`
- `VPUB_VOTE_FAIL_PATTERNS`
- `VPUB_DISAGREEMENT_PATTERNS`
- `VPUB_LOG_WARN_PATTERNS`
- `VPUB_LOG_CRIT_PATTERNS`

---

## R-004 — Reference Oracle 게시 주기

**질문**: reference-oracle-publisher 가 vote 를 보내는 주기는 어느 정도인가? (분 단위 / 시간 단위 / epoch 단위?)

**Why it matters**: VpubOracleStaleVote 알람의 임계 (`> 7200`s 기본) 적정성. 사이클이 30분이면 7200s 는 4 사이클 → 너무 늦음.

**확정 방법**:
```bash
# 6시간 가량 가동 후, oracle 로그에서 ok vote 의 timestamp 분포
grep -i "vote.*ok\|published\|submitted" \
  /home/admin/v-publisher/log/reference-oracle-publisher/$(date -u +%Y%m%d) \
  | awk '{print $1, $2}' | head -30
```

**확정 결과**: _(채움 — 평균/median 사이클)_

**영향**: alerts.md 의 `VpubOracleStaleVote` 임계.

---

## R-005 — 메인넷 RPC Quorum 정확값

**질문**: bridge voter 가 메인넷에서 vote 를 보내기 위해 동의해야 하는 RPC 개수의 정확한 값? (7개 중 4개? 5개? 다수결?)

**Why it matters**: `VpubBridgeRpcMajorityDown` 의 임계 (`< 4`) 정확성.

**확정 방법**:
- README 또는 binary --help 확인
- 일부 RPC 의도적 down 시켜서 voter 동작 관찰 (testnet 안전 시험)
- Jeff/HF 텔레그램 질문 (필요 시)

**확정 결과**: _(채움)_

**영향**: alerts.md 의 `VpubBridgeRpcMajorityDown` expr.

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

- [ ] R-001 ~ R-007: testnet 가동 후 확정
- [x] R-008: ✅ 확정 (독립 module path)
- [x] R-009 ~ R-012: best practice 결정 완료
