# vpub-exporter — Agent Context

> 이 폴더는 **hl-validator-publisher 외부 모니터링 Prometheus exporter** 코드를 작성하는 곳이다.
> Claude Code 가 이 폴더에서 작업할 때 본 문서를 진입점으로 읽고, 아래 파일들로 분기한다.
> 본 프로젝트는 **Spec-Kit (Specification-Driven Development)** 워크플로우를 따른다.

## Spec-Kit Layout

```
vpub-exporter/
├── CLAUDE.md                              # 본 파일 — agent 진입점 (지도)
├── README.md                              # 운영자용 — quickstart + 모든 메트릭/알람 + 트러블슈팅
├── .specify/
│   ├── feature.json                       # 현재 활성 feature directory
│   └── memory/constitution.md             # 프로젝트 헌법 (7원칙)
├── monitoring/                            # monitoring 레포 staging (PR 시 그대로 복사)
│   ├── agents/Test_hyperliquid_VPUB_apn1.toml
│   └── rules/hyperliquid_vpub_rule.yaml   # 22 rules (Tier 0+1, testnet/mainnet 분기)
├── systemd/vpub-exporter.service          # PrivateTmp=no, ProtectSystem=full
├── env/vpub-exporter.env.example
└── specs/001-vpub-exporter/
    ├── spec.md                            # WHAT / WHY (user stories, FR-001~012a, SC-001~008)
    ├── plan.md                            # HOW (tech context, structure)
    ├── research.md                        # Phase 0 (R-001~012 / R-001~004 ✅ 확정)
    ├── data-model.md                      # Phase 1 (도메인 엔티티)
    ├── contracts/
    │   ├── metrics.md                     # 메트릭 인터페이스 명세 + 코드 cross-ref
    │   └── alerts.md                      # 알람 룰 명세 (22 rules)
    ├── quickstart.md                      # 검증 시나리오 QS-1~6 (LSN-D13958 운영 결과 반영)
    ├── checklists/requirements.md         # spec quality + 운영 검증 status
    └── tasks.md                           # Phase 1-5 + Polish task 매트릭스
```

<!-- SPECKIT START -->
**Active plan**: `specs/001-vpub-exporter/plan.md`
<!-- SPECKIT END -->

## 어디서부터 읽는가 (사용 시나리오별)

| 작업 | 먼저 읽기 |
|---|---|
| 처음 들어옴 / 전체 파악 | `spec.md` → `plan.md` (10분) |
| 새 메트릭 추가 / 변경 | `contracts/metrics.md` → `spec.md` FR 매칭 → 코드 |
| 새 알람 룰 추가 / 변경 | `contracts/alerts.md` → `spec.md` US 매칭 |
| 가동 첫날 로그 패턴 확정 | `research.md` R-001~007 |
| 검증 시나리오 실행 | `quickstart.md` |
| 원칙 / 코딩 룰 확인 | `.specify/memory/constitution.md` |
| Tasks 분해 / 구현 시작 | `tasks.md` (없으면 `/speckit.tasks` 로 생성) |

## 사용자 컨펌 (2026-05-23)

1. ✅ Port 8002
2. ✅ Publisher 머신: LSN-D13958 / 64.31.51.41 / x86_64 / Tokyo / testnet user=admin
3. ✅ Outcome pending count 는 fallback (단순 24h 메시지 수)
4. ✅ 범위는 Tier 0 + 1 + 2 전부 한 코드베이스
5. ✅ Testnet 은 PagerDuty 호출 X — critical 룰 6건 mainnet 한정 + testnet 복제 (high)
6. ✅ GitHub: `nodebreaker0-0/vpub-exporter` (private). monitoring 레포는 별도 B-Harvest 내부 레포.

## 운영 검증 결과 (2026-05-23)

Tier 0 MVP testnet 가동 ✅:
- `VpubServiceDownTestnet` 정확히 발화 + resolve (90s 임계 SC-001 합격)
- `VpubChildMissing` — publisher robust spawn 으로 발화 X (안전망 룰로 재정의 — spec.md SC-002 보정)
- `VpubLogStale` — 7분 (5m 임계 + 2m for) 이상 stall 시 정상 발화
- 모든 메트릭 정상 노출 + 시크릿 누출 0 + `/metrics` p95 < 5ms
- `vpub_oracle_vote_total{ok}` = 12122 (testnet 가동 ~1시간)

## 절대 금지 (Constitution II / IV 직결)

- ❌ publisher 의 config / 상태 / 로그 변형 (read-only)
- ❌ 자동 outcome vote / 자동 unjail / 자동 restart
- ❌ Slack token / RPC API key / agent key 등 시크릿을 코드 / 주석 / 메트릭 값 / 라벨 / 문서에 노출
- ❌ alertmanager 라우팅 5종(`critical` / `high` / `medium` / `low` / `disk`) 외의 alert_level 사용
- ❌ config.json 직접 파싱 (필요한 값은 별도 env 변수로)
- ❌ 검증 없이 메인넷 IP / URL 하드코딩

## 외부 참조

- 워크스페이스 hl-agent skill 의 진입점: `/var/folders/.../skills/hl-agent/SKILL.md`
- hl-validator-publisher README: `../hl-validator-publisher/README.md`, `README.ko.md`
- 사용자 service 파일: `../hl-validator-publisher/v-publisher.service`, `v-publisher.service.full`
- 통합 monitoring 레포: `../monitoring/` (별도 git repo)
- 기존 exporter / monitor 컨벤션 참고: `../mainnet/hl-exporter/`, `../mainnet/hlmon/`, `../testnet/hlmon/`
- hl-node 데이터 폴더 정의: `../NODE_DATA_LAYOUT.md`
- HL 공식 docs: `https://hyperliquid.gitbook.io/hyperliquid-docs`
- Publisher binary (testnet): `https://binaries.hyperliquid-testnet.xyz/validator-publisher/`

## Workflow 명령 (참고)

이 프로젝트는 spec-kit CLI 가 아니라 **수동 워크플로우**로 운영. 다음 단계는 사용자가 명시적으로 트리거:

1. ✅ `constitution` — 작성됨 (`.specify/memory/constitution.md`)
2. ✅ `specify` — 작성됨 (`specs/001-vpub-exporter/spec.md`)
3. ⏳ `clarify` — 필요 시 (현재 NEEDS CLARIFICATION 없음, research.md 의 R 항목으로 이관됨)
4. ✅ `plan` — 작성됨 (`specs/001-vpub-exporter/plan.md`, `data-model.md`, `contracts/`, `quickstart.md`)
5. ⏳ `tasks` — 다음 단계. 사용자가 Claude Code 호출 시 `/speckit.tasks` 또는 동등한 지시로 생성
6. ⏳ `analyze` — 선택. tasks 완료 후 cross-artifact 일관성 검사
7. ⏳ `implement` — 코드 작성. tasks.md 의 phase 별 진행
