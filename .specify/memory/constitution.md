# vpub-exporter Constitution

## Core Principles

### I. Outside-the-Box Monitoring (NON-NEGOTIABLE)

vpub-exporter는 hl-validator-publisher **외부**에서 동작한다. publisher 바이너리가 자체적으로 보내는 Slack 알람에 의존하지 않는다. 바이너리가 죽으면 알람도 못 옴 — 그 공백을 메우는 것이 본 도구의 존재 이유. 따라서 publisher 가 죽었다는 사실 자체를 외부에서 항상 관찰 가능해야 한다.

### II. No Side Effects on Publisher

vpub-exporter 는 **read-only**. publisher 의 config / 상태 / 로그를 절대 변형하지 않는다. 자동 unjail / 자동 vote / 자동 restart 같은 액션은 일절 금지. 슬래시 위험을 0으로 유지하기 위해 모든 vote/restart 결정은 사람이 한다.

### III. Monitoring 레포 Convention 준수 (NON-NEGOTIABLE)

이 도구는 B-Harvest 의 기존 `monitoring/` 레포 (Prometheus + Alertmanager + Ansible) 와 통합된다. 따라서:

- 메트릭 prefix 는 `vpub_` 고정 (기존 `aqa_publisher_*`, `hlmon_*` 패턴 일관성)
- alert_level 은 `critical`, `high`, `medium`, `low`, `disk` 5종 안에서만 (alertmanager 라우팅 호환)
- 룰 파일은 `promtool check rules` 통과 필수
- agent TOML 형식은 `monitoring/config/agents/Main_hyperliquid_F1_IDC.toml` 와 동일 구조

### IV. Secrets Never in Code / Metrics / Labels

Slack bot token / agent private key / RPC API key / PagerDuty key 등 모든 시크릿은:

- 코드/주석/문서에 평문 출력 금지
- 메트릭 값에 노출 금지 (실수로 token 일부가 label 에 들어가는 사고 방지)
- 라벨에 노출 금지
- env 파일 (0600) 으로만 주입
- config.json 직접 파싱 금지 (필요한 값은 별도 env 변수로 주입)

### V. Non-Blocking Scrape

`/metrics` HTTP 핸들러는 **즉시 응답**해야 한다. 외부 호출 (Slack API, Arbitrum RPC, HTTP HEAD) 은 별도 goroutine 으로 주기 실행 후 결과를 캐시. scrape 시점에 외부 의존성 호출 X. timeout 명시 (Slack 5s, RPC 5s, HTTP HEAD 10s).

### VI. Time-Sensitive Truth from Logs

publisher 의 실제 동작 (vote 성공, RPC disagreement) 은 **로그 파일 tail + 패턴 매칭**으로 추정한다. 정확한 로그 패턴은 가동 첫날 실제 출력 보고 확정. 코드에는 default 패턴을 두되 env 로 오버라이드 가능하게. **로그 포맷 변경 = 메트릭 단절** 위험 → 가동 후 로그 패턴 회귀 테스트 필수.

### VII. Tier Gating

기능은 Tier 0 → Tier 1 → Tier 2 순으로 출시한다. Tier 0 (process / child / log mtime) 가 완성되기 전엔 Tier 1 시작 금지. 운영 가치는 Tier 0 만으로도 이미 의미가 있어야 한다 (MVP).

## Operational Constraints

- **Target platform**: Linux amd64 (Tokyo region publisher 머신)
- **Co-located**: vpub-exporter 와 validator-publisher.service 는 **같은 머신**에 배치 (로그 파일 직접 접근)
- **Port**: 8002 (aqa-publisher-exporter 8001 다음 자리)
- **User**: 가능하면 publisher 와 동일한 systemd user (`admin` testnet / `ubuntu` mainnet) — 로그 read 권한 단순화
- **Resource budget** (R-027 정정 2026-06-08):
  - testnet / mainnet 모두 cgroup `MemoryMax` / `CPUQuota` 적용 안 함 — 옛 testnet 200MB 한계가 v0.3.1 binary startup 메모리와 충돌 → fork EAGAIN 무한 재시작 발생.
  - 대신 **soft constraint**: collection_duration_p95 (SC-003) + `systemctl show ... MemoryCurrent` 로 모니터링.
  - 한계 다시 필요 시 R-027b — 측정 후 적정값 (testnet ≥ 500MB / mainnet 1GB+) 으로 재적용.
- **systemd 격리** (R-026 + R-027 정정 2026-06-08):
  - 사용자 운영 표준 (R-027): simplified unit — `User=admin/ubuntu`, `WorkingDirectory`, `ExecStart`, `Restart=on-failure`, **`ReadOnlyPaths=/home/<user>/v-publisher`** (Constitution II 핵심), `LimitNOFILE=65536`. 다른 sandbox / cgroup directive 모두 제거.
  - R-026 표준 운영 (`--log-dir log` 사용 시 publisher home 의 `log/<component>/`) 에서는 옛 `PrivateTmp=no` (옛 R-001 fallback 보호) 도 불필요.
  - 옛 hardened 형태 (NoNewPrivileges/ProtectSystem/MemoryDenyWriteExecute 등 다수 sandbox) 는 README "Hardened systemd alternative" 섹션에 참고용으로 보존 — defense-in-depth 가 필요한 환경에서 옵션.
  - systemd dbus property 조회 시 `MainPID`/`NRestarts` 는 `Service` interface 에서 (`GetUnitTypePropertiesContext`). `Unit` interface 로는 None 반환.
- **Alertmanager 분기 정책**: critical 6 룰은 mainnet 한정 (`network!="testnet"` matcher). 동일 expr 의 `<Name>Testnet` 복제가 alertLevel=high 로 따로 발화 — PagerDuty 노이즈 차단.

## Development Workflow

1. **Spec-driven**: `specs/001-vpub-exporter/spec.md` 가 single source of truth. WHAT/WHY 가 바뀌면 spec 부터.
2. **Constitution check**: `plan.md` 작성/수정 시 본 문서의 7 원칙에 대한 ✅ 통과 표 명시.
3. **Branching**: monitoring 레포는 `feat/vpub-exporter` 단일 PR (Tier 0 만 먼저 머지, Tier 1/2 는 후속 PR).
4. **Verification gate**: 코드 머지 전에 `quickstart.md` 의 검증 시나리오 통과.

## Governance

본 헌법은 vpub-exporter 의 모든 design / code / PR 결정에 우선한다. 위반 시 `plan.md` 의 Complexity Tracking 표에 사유 명시 + builnad 승인. 슬래시 위험과 관련된 원칙 (II, IV) 은 예외 없음.

**Version**: 1.0.0 | **Ratified**: 2026-05-23 | **Last Amended**: 2026-05-23
