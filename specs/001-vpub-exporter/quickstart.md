# Quickstart — vpub-exporter 검증 시나리오

> 본 문서는 vpub-exporter 가 spec.md 의 Success Criteria (SC-001 ~ 008) 를 만족하는지 testnet/mainnet 환경에서 직접 확인하기 위한 시나리오 모음이다.
> 각 시나리오는 **재현 절차 + 기대 결과 + 합격 기준**을 갖는다.

## 사전 조건

1. publisher 머신에 vpub-exporter 바이너리 + systemd unit + env 파일 배포 완료
2. `systemctl start vpub-exporter` 후 `curl localhost:8002/metrics` 가 `vpub_service_up` 응답
3. monitoring 레포 PR 머지 → Prometheus 서버가 새 인스턴스 스크레이프 + 룰 로드 완료
4. Slack 알람 채널 (`#ddoa-critical`, `#ddoa-high`, `#ddoa-low`) 워치 중
5. testnet 가동 환경 (메인넷 검증은 별도 — 합성 실패 시나리오 위험)

---

## QS-1 — Tier 0 / US1 / SC-001, SC-002, SC-006

### QS-1.1 — Publisher 정지 → critical 알람 (SC-001)

**절차**:
```bash
sudo systemctl stop v-publisher.service
date -u    # 정지 시각 기록
# Slack #ddoa-critical 알람 도착 시각 기록
sudo systemctl start v-publisher.service
```

**기대**:
- `vpub_service_up` 메트릭이 0 으로 떨어짐
- `VpubServiceDown` 알람 발화 (PagerDuty + Slack critical 채널)
- 정지 → 알람 도착 평균 < 90초, P95 < 120초

**합격**: 5회 반복 평균 시간이 90초 이하.

### QS-1.2 — Child 1개 죽임 → critical (SC-002)

**절차**:
```bash
# visor PID 확인
VISOR_PID=$(systemctl show -p MainPID --value v-publisher)
# child 한 개 (예: bridge-voter) 강제 종료
CHILD_PID=$(pgrep -P $VISOR_PID | head -1)
sudo kill -9 $CHILD_PID
date -u
```

**기대**:
- `vpub_child_count` 가 2 로 떨어짐 (visor 가 즉시 재spawn 하지 않는다는 가정 — 그렇다면 임계 30s 조정 필요)
- `VpubChildMissing` 알람 발화

**합격**: 평균 < 60초, P95 < 90초.

### QS-1.3 — 로그 stall 시뮬레이션 (US1-3)

**절차**: publisher 가 작동 중인 상태에서 컴포넌트 로그 파일을 임의로 `chmod 000` (쓰기 막음) → 5분 대기 → 복원

**기대**: `VpubLogStale` (high) 알람 발화 후, 30분 유지 시 `VpubLogStaleLong` (critical) 으로 escalate

**합격**: 5분 임계 + 2분 `for` 후 알람 도착.

---

## QS-2 — Tier 1 / US2

### QS-2.1 — RPC Quorum 위태 (US2-1)

**절차**: testnet 머신에서 `iptables` 로 RPC 호스트 중 4개 차단 (메인넷 모의)

**기대**: 5분 후 `VpubBridgeRpcMajorityDown` (high)

**합격**: 알람 발화 + 차단 해제 후 5분 내 resolve.

### QS-2.2 — Bridge Stale Vote (US2-2)

**절차**: testnet 에서 Arbitrum 입금이 1시간 가량 없는 시간대 활용 (또는 모든 RPC 응답을 fake 값으로 변경하여 의도적 stale 유도 — 위험)

**기대**: 1시간 후 `VpubBridgeStaleVote` (high)

**합격**: 알람 발화. **참고**: testnet 입금 트래픽 적어 false positive 가능 — 임계는 가동 1주 후 조정 (R-004 참조).

### QS-2.3 — Disagreement (US2-3)

**절차**: 두 RPC URL 을 의도적으로 서로 다른 Arbitrum 노드 (혹은 fork) 로 설정

**기대**: 15분 내 5건 이상 disagreement 카운트 → `VpubBridgeRpcDisagreement` (high)

**합격**: 알람 발화. **위험**: 실제 bridge voter 가 잘못된 vote 보낼 수 있음 → testnet 만, 짧게.

### QS-2.4 — Slack Token Invalid (US2-6, SC-003)

**절차**: env 의 `VPUB_SLACK_BOT_TOKEN` 을 가짜 값으로 교체 → exporter 재시작

**기대**: `vpub_slack_api_ok` = 0, 5분 후 `VpubSlackTokenInvalid` (critical)

**합격**: 알람 발화. 원래 토큰 복원 후 resolve.

### QS-2.5 — Outcome Pending (US2-5)

**절차**: testnet 에서 outcome 채널에 자연스럽게 메시지 누적 대기. 또는 같은 채널에 운영자가 더미 메시지 6건 게시.

**기대**: `vpub_outcome_slack_msg_24h` > 5, 30분 후 `VpubOutcomePendingLong` (medium)

**합격**: 알람 발화.

---

## QS-3 — Tier 2 / US3

### QS-3.1 — Binary Update Available (US3-1)

**절차**: 더미 HTTP 서버 (`python -m http.server`) 띄우고, 거기에서 `Last-Modified: <future>` 헤더로 응답하도록 설정 → `VPUB_BINARY_URL` 변경 → exporter 재시작

**기대**: `vpub_binary_remote_mtime_unix` > local + 3600, 30분 후 `VpubBinaryUpdateAvailable` (medium)

**합격**: 알람 발화.

### QS-3.2 — Remote Check Fail (US3-2)

**절차**: `VPUB_BINARY_URL` 을 존재하지 않는 호스트로 변경 → exporter 재시작

**기대**: 1시간 후 `VpubBinaryRemoteCheckFail` (low)

**합격**: 알람 발화.

---

## QS-4 — 성능 / 자원 (SC-003, SC-004)

### QS-4.1 — `/metrics` 응답 시간 P95 < 200ms

**절차**:
```bash
for i in {1..100}; do
  curl -o /dev/null -w "%{time_total}\n" -s http://localhost:8002/metrics
  sleep 1
done | sort -n | awk 'BEGIN{c=0} {a[c++]=$1} END{print "p95:", a[int(c*0.95)]}'
```

**기대**: p95 < 0.2s

**합격**: 안정 가동 1시간 후 측정.

### QS-4.2 — RSS / CPU

**절차**:
```bash
# 1시간 모니터링
pid=$(systemctl show -p MainPID --value vpub-exporter)
while true; do
  ps -o rss,pcpu -p $pid | tail -1
  sleep 60
done | tee /tmp/vpub-resource.log
```

**기대**: RSS 평균 < 100MB, CPU 평균 < 5% (1 core)

**합격**: 1시간 데이터의 평균이 임계 이하.

---

## QS-5 — Read-only 보존 (SC-005)

### QS-5.1 — publisher 파일 무변경

**절차**: vpub-exporter 가동 1시간 동안 `/home/admin/v-publisher/` 의 config.json / 바이너리 / 로그 파일 mtime/size 가 vpub-exporter 의 동작에 의해 변경되지 않는지 확인 (logrotate / publisher 자신의 갱신은 제외).

```bash
sudo find /home/admin/v-publisher -type f \
  ! -newer /tmp/vpub-start-mark \
  -printf "%T@ %s %p\n" | sort -n > /tmp/before.txt
# 1시간 후
sudo find /home/admin/v-publisher -type f \
  ! -newer /tmp/vpub-now-mark \
  -printf "%T@ %s %p\n" | sort -n > /tmp/after.txt
diff /tmp/before.txt /tmp/after.txt
```

**기대**: vpub-exporter 가 만든 변경 0건 (publisher 자체 갱신은 expected diff).

**합격**: lsof / strace 로 vpub-exporter 의 fopen 모드가 모두 O_RDONLY 인지 추가 검증.

---

## QS-6 — Constitution 회귀

가동 첫날 + 첫 PR 머지 시 본 항목들 직접 확인:

- [ ] `vpub_service_up`, `vpub_child_count`, `vpub_component_log_mtime_seconds` 가 `/metrics` 에 정상 노출 (Tier 0)
- [ ] 모든 시크릿 env 값 grep 결과가 `/metrics` 본문 / `journalctl -u vpub-exporter` / 코드 검색에서 0 hit
- [ ] alert_level 라벨이 `critical`/`high`/`medium`/`low`/`disk` 5종 안에 있음 (룰 파일 grep)
- [ ] `promtool check rules monitoring/config/rules/hyperliquid_vpub_rule.yaml` 통과
- [ ] vpub-exporter 의 systemd `ProtectSystem=full` + `ReadOnlyPaths=` 로 publisher 디렉토리 RO 보장
- [ ] 외부 호출 timeout 명시 (RPC 5s, Slack 5s, HEAD 10s) — 코드 grep
