# Specification Quality Checklist — vpub-exporter

**Purpose**: spec.md / plan.md / contracts/ / research.md 가 implement 단계로 가기 전 품질 통과 확인
**Created**: 2026-05-23
**Feature**: `/specs/001-vpub-exporter/spec.md`

## Content Quality

- [x] spec.md 에 implementation 세부 (언어/프레임워크/포트 등) 가 누설되지 않음 — Go/8002 같은 기술 항목은 plan.md 에만 있음
- [x] user value / 운영 가치 중심으로 작성됨 (왜 외부 모니터가 필요한가, 어떤 실패 모드를 잡는가)
- [x] 비기술 stakeholder (예: 다른 validator 운영자) 가 읽을 수 있는 수준
- [x] 모든 mandatory 섹션 완료 (User Scenarios, Requirements, Success Criteria, Assumptions)

## Requirement Completeness

- [x] `[NEEDS CLARIFICATION]` 마커가 spec.md 본문에 잔존하지 않음 (모든 모호함은 research.md 의 R-001~007 로 이관)
- [x] 모든 FR 이 testable / unambiguous
- [x] success criteria 가 measurable (시간/비율/카운트)
- [x] success criteria 가 technology-agnostic (SC-001~002, 005~007 은 운영 가치, SC-003~004 는 자원 — 일부 implementation 함의 있지만 사용자 가시 효과 기준)
- [x] 모든 acceptance scenario 정의됨 (US1: 4, US2: 6, US3: 2)
- [x] edge case 식별됨 (디스크 fill, 트래픽 없는 시간대, prom 서버 단절, 로그 회전, slack rate limit)
- [x] scope 명확히 경계됨 (publisher 만 — node validator 영역은 별도 hl-exporter)
- [x] dependency / assumption 식별됨 (publisher dedicated machine, monitoring 레포 가용성, slack token 재사용 등)

## Feature Readiness

- [x] 모든 FR 이 contracts/ 의 메트릭/알람과 cross-ref (metrics.md §F)
- [x] user story 가 primary flow 커버 (P1 publisher down, P2 본업 모니터, P3 업그레이드)
- [x] feature 가 SC 의 measurable outcome 을 만족할 수 있는 설계 (plan.md + contracts/)
- [x] spec.md 에 implementation 디테일 누설 없음

## Constitution Alignment (vpub-exporter Constitution)

- [x] I. Outside-the-Box Monitoring — US1 자체가 이 원칙의 직접 검증
- [x] II. No Side Effects — FR-016 으로 명시
- [x] III. Monitoring 레포 Convention — FR-018/019, plan.md Structure Decision, alerts.md §0
- [x] IV. Secrets — FR-015, plan.md Key Rules
- [x] V. Non-Blocking Scrape — FR-017, plan.md Performance Goals
- [x] VI. Time-Sensitive Truth from Logs — FR-020, research.md R-003
- [x] VII. Tier Gating — User Story P1/P2/P3, tasks 는 후속 명령

## 운영 검증 (2026-05-23 LSN-D13958 testnet 가동 후)

### Tier 0 합격 매트릭스

- [x] **SC-001** Publisher stop → critical 알람 < 90s (실측 ≤ 90s, resolve 도 정상)
- [x] **SC-002 (재정의)** `VpubChildMissing` 안전망 룰 — publisher robust spawn 으로 정상 운영 중 발화 0건 = pass
- [x] **SC-003** `/metrics` 응답 p95 < 200ms (실측 ~4ms)
- [x] **SC-004** RSS < 100MB (실측 8.7MB), CPU < 5% (확인됨)
- [x] **SC-005** Read-only 보존 (publisher 파일 변경 0)
- [ ] **SC-006** 1개월 down 감지율 100% (운영 누적 필요)
- [ ] **SC-007** false-positive < 10% (1주일 데이터 후 재평가)
- [x] **SC-008** Tier 0 첫 PR → 배포 < 24h ✅

### 운영 발견 (백포트 완료)

- [x] R-001/002/003/004 ✅ testnet 9.7h 실로그 분석으로 확정 + env.example default 갱신
- [x] systemd dbus `MainPID`/`NRestarts` Service interface 분리 호출
- [x] systemd unit `PrivateTmp=no` (publisher `/tmp/...` read 위해)
- [x] `VpubLogStale`/`Long` 에 `component!="visor"` 매처 (자체 로그 빈도 낮음)
- [x] critical 6 룰 mainnet 한정 + testnet 복제 (`<Name>Testnet`, high) → PagerDuty noise 차단
- [x] `VpubBridgeStaleVote` mainnet only (testnet 입금 0건 자연 false-positive 차단)
- [x] yamllint indent + parser wrap 시뮬 promtool check 모두 통과

### 후속 (mainnet 가동 시점)

- [ ] R-005 메인넷 RPC quorum 정확값 확정 (vote 시도 시 합의 라인 관찰)
- [ ] R-006 메인넷 binary URL (HF announce)
- [ ] mainnet 인스턴스 추가 PR (별도 agent TOML, network=mainnet)
- [x] **R-013 (2026-06-06 mainnet 2.6h)** `VpubBridgeRpcDisagreement` 룰 삭제 — publisher 가 "disagreement" 단어 자체를 안 찍음. 옛 메트릭은 사실 provider HTTP error 카운터. → `vpub_bridge_rpc_provider_fail_total{name, status_code}` rename.
- [ ] Tier 1 임계 false-positive 비율 1주 데이터 후 재평가
