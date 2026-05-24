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
sudo systemctl stop validator-publisher.service
date -u    # 정지 시각 기록
# Slack #ddoa-critical 알람 도착 시각 기록
sudo systemctl start validator-publisher.service
```

**기대**:
- `vpub_service_up` 메트릭이 0 으로 떨어짐
- `VpubServiceDown` 알람 발화 (PagerDuty + Slack critical 채널)
- 정지 → 알람 도착 평균 < 90초, P95 < 120초

**합격**: 5회 반복 평균 시간이 90초 이하.

### QS-1.2 — Child 1개 죽임 → child_count 회복 관찰 (SC-002 재정의)

> **2026-05-23 LSN-D13958 운영 보정**: publisher (visor) 가 child 가 죽으면 **1-3 초 안에 즉시 재spawn** 한다 (검증됨 — PID 2567261 kill → 2567734 로 재시작). 따라서 우리 `vpub_child_count < 3` 의 `for: 30s` 임계로는 detect 못 함. 이건 룰 버그가 아니라 **publisher 의 robustness** 때문. `VpubChildMissing` 은 "visor 가 살아있지만 재spawn 도 못하는 매우 드문 비정상 상태" 를 잡는 **안전망 룰** 로 재정의한다.

**절차**:
```bash
# visor PID 확인
VISOR_PID=$(systemctl show -p MainPID --value validator-publisher)

# child 한 개 (bridge-voter) 강제 종료
CHILD_PID=$(pgrep -P $VISOR_PID -f bridge-voter)
echo "kill $CHILD_PID at $(date -u)"
sudo kill -9 $CHILD_PID

# visor 가 즉시 재spawn 하는지 5초 후 확인 (publisher 정상 = 재spawn O)
sleep 5
ps --ppid $VISOR_PID -o pid,cmd
# 기대: bridge-voter 가 새 PID 로 다시 떠 있음 — 알람 X 가 정답
```

**기대**:
- 5초 이내 새 child PID 로 재spawn → `vpub_child_count` 가 2 로 떨어지는 윈도우 < 30초 → **`VpubChildMissing` 알람 발화 X** (정상)
- 만약 알람이 발화한다면 → visor 자체에 이상 (재spawn 실패) → 실제로 알람 의미 있음

**합격 (재정의)**:
- 정상 운영 중 `VpubChildMissing` 발화 0건 = pass (안전망 룰의 정상 상태)
- `VpubServiceDown` (QS-1.1) 이 SC-002 의 대체 시그널 역할 — visor 자체가 죽거나 systemctl 이 active 상태 못 유지하면 그쪽이 먼저 발화

**원본 SC-002 검증 불가 이유**: spec.md 초안 작성 시 visor 의 spawn 동작을 모른 채로 "5초 안에 child 사망 감지" 시나리오를 가정. 실제 publisher 는 더 robust 함 — Tier 0 의 child_count 메트릭은 안전망 가치로 유지하되 합격 기준은 "알람 안 뜨면 정상" 으로 변경.

### QS-1.3 — 로그 stall 시뮬레이션 (US1-3)

**절차**: publisher 가 작동 중인 상태에서 컴포넌트 로그 디렉토리를 임의로 `chmod 000` (쓰기 막음) → **8분 이상** 대기 → 복원

```bash
sudo chmod 000 /tmp/validator-publisher/bridge-voter
echo "block at $(date -u)"
sleep 480   # 5분 임계 + 2분 for + 1분 마진
# → #ddoa-high 채널에 VpubLogStale (alertEvent vpub:log:stale) 발화 기대
sudo chmod 755 /tmp/validator-publisher/bridge-voter
echo "restored at $(date -u)"
# 복원 후 alertmanager resolve 메시지 도착 기대
```

**기대**: `VpubLogStale` (high) 알람 발화 후, 30분 유지 시 `VpubLogStaleLongTestnet` (high, testnet 분기됨) / `VpubLogStaleLong` (critical, mainnet) 으로 escalate

**합격**: 5분 임계 + 2분 `for` 합 7분 이상 후 알람 도착. 8분 sleep 권장.

**주의**: 사이드 발견 — testnet 가동 1시간 후엔 `VpubBridgeStaleVote` (1h 임계) 가 자연 발화. **이 룰은 mainnet only 로 정정됨** (testnet 입금 트래픽 0 으로 인한 false-positive 차단).

---

## QS-2 — Tier 1 / US2

### QS-2.1 — RPC Quorum 위태 (US2-1)

**절차**: testnet 머신에서 `iptables` 로 RPC 호스트 중 일부 차단 (testnet RPC max=3 이라 2 차단 시 1만 살아남음 → `< 2` 매처 발화. mainnet `< 4` 매처는 mainnet 가동 후 검증).

**기대**: 5분 후 `VpubBridgeRpcMajorityDownTestnet` (high) — testnet 복제 룰

**합격**: 알람 발화 + 차단 해제 후 5분 내 resolve.

### QS-2.1m — R-005 mainnet quorum 정확값 확정 (mainnet 가동 직후)

**왜 필요한가**: mainnet 7 RPC quorum 임계 `< 4` 는 운영자 가설. HF 가 실제 vote_majority 로 요구하는 minimum live RPC count 가 가설과 다를 수 있음 — 가동 첫 vote 시도 시 실측 필요.

**절차**:
1. 첫 vote 시점 직전부터 `vpub_bridge_rpc_up` 의 7 시리즈 상태 캡처:
   ```bash
   curl -s localhost:8002/metrics | grep "^vpub_bridge_rpc_up"
   # 7개 시리즈 모두 1 이어야 정상
   ```
2. 첫 vote 성공 후 publisher 로그에서 quorum 관련 메시지 확인:
   ```bash
   sudo grep -E "votes_sent|quorum|insufficient.*rpc" /home/ubuntu/v-publisher/log/$(date -u +%Y%m%d) | head -20
   ```
3. RPC 를 한 번에 하나씩 차단해 가며 vote 가 계속 성공하는 최소 N 확인:
   - 차단 절차: `sudo iptables -A OUTPUT -d <rpc-host> -j DROP` (해제 `-D`)
   - 각 단계 후 30분 관찰 → 다음 vote 가 ok 인지 확인
   - N+1 번째 차단 시 vote 실패가 처음 발생 → quorum = N
4. 룰 정정 (필요 시):
   ```yaml
   # monitoring/rules/hyperliquid_vpub_rule_tier1.yaml
   - alert: VpubBridgeRpcMajorityDown
     expr: count(vpub_bridge_rpc_up{network!='testnet',disable_alarm!='true'} == 1) < <N>
   ```
5. research.md R-005 에 확정값 + 검증 로그 백포트.

**위험**: 실제 RPC 차단 시 bridge voter 의 quorum 깨질 수 있음 → vote 실패 = 운영 데미지 가능. **vote 가 없는 quiet 시간대** (입금 트래픽 적은 시간) 에 실행 권장. 가능하면 dry-run 으로 prometheus `vpub_bridge_rpc_up == 1` count 만 만들어 알람 발화 시점만 확인.

### QS-2.1p — PagerDuty dry-run (mainnet 가동 전)

**왜 필요한가**: critical 6 룰 (`VpubServiceDown`, `VpubChildMissing`, `VpubLogStaleLong`, `VpubBridgeStaleVoteLong`, `VpubOracleStaleVoteLong` ×2 chain) 은 mainnet only. 가동 직후 셋업이 잘못되면 운영자 전원이 PagerDuty 호출. dry-run 필요.

**절차**:
1. monitoring 레포의 alertmanager.toml 에서 임시 라우팅 추가:
   ```toml
   [[slacks]]
   enabled = true
   targetAlertLevels = ["critical"]
   filters = ['network=mainnet', 'chain=hyperliquid']
   resendDuration = "1m"
   channel = "C08M18L34BD"  # ddoa-testalrt — PagerDuty 대신
   ```
   기존 PagerDuty entry 는 일시 disable.
2. mainnet 인스턴스에서 일부러 publisher 중지 30s:
   ```bash
   sudo systemctl stop validator-publisher
   sleep 35
   sudo systemctl start validator-publisher
   ```
3. 30s 내 `VpubServiceDown` (critical, mainnet) firing → testalrt 채널 도착 확인.
4. 만족스러우면 alertmanager.toml 원복 (PagerDuty 활성, testalrt entry 제거), prometheus reload.

**합격**: testalrt 채널에서 메시지 수신 + resolve 메시지도 1m 안에 옴.

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

## QS-3 — Tier 2 / US3 (R-019 per-component binary tracking)

### QS-3.1 — visor 업데이트 알람 (US3-1a)

**절차** — local visor mtime 을 절대 시각 1970 으로 만들어 remote 보다 무조건 과거가 되게:

```bash
# (옵션) 원래 mtime 백업
sudo stat -c '%y' /home/admin/v-publisher/visor | sudo tee /root/visor.mtime.bak

# local 을 1970-01-02 로
sudo touch -t 197001020000 /home/admin/v-publisher/visor
sleep 130   # 60s collector tick + 1m for + alertmanager lookahead 안전 margin

# 알람 확인
curl -s 'http://<prom>:9090/api/v1/alerts' \
  | jq '.data.alerts[] | select(.labels.alertname=="VpubVisorBinaryUpdateAvailable")'
```

**기대**: 슬랙 메시지 (`alertLevel: medium`, testnet/mainnet 동일):
> :red_circle: vpub: visor binary 업데이트 announced
> testnet visor binary at https://binaries.hyperliquid-testnet.xyz/validator-publisher/visor has been updated.
> New Last-Modified: 2026-05-22 03:50:10 +0000 UTC
> local mtime:      1970-01-02 00:00:00 +0000 UTC

**원복** → 자동 resolve:
```bash
sudo touch -d "$(cat /root/visor.mtime.bak)" /home/admin/v-publisher/visor
# 60s 안에 local mtime 갱신 → expr 음수 → alertmanager resolved 발송
```

**합격**: (a) 알람 firing 후 (b) 원복 시 resolved 메시지.

### QS-3.2 — child download 실패 알람 (US3-1b)

**전제**: visor 가 살아 있고 한 번이라도 `INFO visor: downloading new binary self.binary_name="<child>"` 로그를 찍은 적이 있어야 함 (= `vpub_binary_download_started_unix{component=<child>}` 시리즈 존재).

**절차** — child file mtime 을 과거로 강제. download_started 는 그대로 → expr 양수:

```bash
sudo stat -c '%y' /home/admin/v-publisher/bridge-voter | sudo tee /root/bv.mtime.bak
sudo touch -t 197001020000 /home/admin/v-publisher/bridge-voter
sleep 130

curl -s 'http://<prom>:9090/api/v1/alerts' \
  | jq '.data.alerts[] | select(.labels.alertname=="VpubChildBinaryDownloadFailed")'
```

**기대**: 슬랙 메시지 (`alertLevel: high`, testnet/mainnet 동일):
> :warning: vpub: bridge-voter 다운로드 실패 (60s+ mtime 미갱신)
> testnet visor 가 bridge-voter download 로그를 찍은 후 60s+ 동안 local mtime 미갱신.
> download log ts: ...
> local mtime: 1970-01-02 00:00:00 +0000 UTC

**원복** → 자동 resolve:
```bash
sudo touch -d "$(cat /root/bv.mtime.bak)" /home/admin/v-publisher/bridge-voter
# 또는 visor 재시작 → visor 가 자동 download → mtime 갱신 → 자동 resolve
sudo systemctl restart validator-publisher
```

**합격**: firing 후 원복 시 resolved.

### QS-3.3 — Remote Check Fail (US3-2)

**절차**: `VPUB_BINARY_URL` 을 존재하지 않는 호스트로 변경 → exporter 재시작 → 10분 대기.

```bash
sudo sed -i 's|^VPUB_BINARY_URL=.*|VPUB_BINARY_URL=https://nonexistent.invalid/visor|' /etc/vpub-exporter.env
sudo systemctl restart vpub-exporter
sleep 700   # for 10m
```

**기대**: `VpubBinaryRemoteCheckFail` (low) 발화. `vpub_binary_remote_check_ok{component="visor"} == 0`.

**원복**: env 복구 후 재시작 → 60s 안에 ok=1 → 자동 resolve.

---

## QS-4 — 성능 / 자원 (SC-003, SC-004)

**실측 합격 (2026-05-24, LSN-D13958 testnet, Phase 6 빌드)**:

| 항목 | 측정 | 임계 | 마진 |
|---|---|---|---|
| `/metrics` p50 / p95 / p99 | 0.86 / **1.36** / 1.49 ms | p95 < 200ms | **147×** |
| RSS avg (5분) | **15.2 MB** | < 100 MB | 6.6× |
| CPU avg (5분) | **0.2 %** | < 5% | 25× |

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

**실측 합격 (2026-05-24)**: `lsof -p <pid>` 결과 publisher 디렉토리 (`v-publisher` / `validator-publisher`) FD count = **0**. binary collector 가 `os.Stat` 만 호출 (file open 없음), bridge_state.json 도 60s 주기 짧은 open-read-close 라 snapshot 에 안 잡힘. write/append FD 도 0. 코드 레벨 read-only 검증 통과.

### QS-5.1 — publisher 파일 무변경

**절차**: vpub-exporter 가동 1시간 동안 `/home/admin/v-publisher/` 의 config.json / 바이너리 / 로그 파일 mtime/size 가 vpub-exporter 의 동작에 의해 변경되지 않는지 확인 (logrotate / publisher 자신의 갱신은 제외).

```bash
sudo find /home/admin/v-publisher -type f -printf "%T@ %s %p\n" | sort -n > /tmp/before.txt
sleep 600   # 10분
sudo find /home/admin/v-publisher -type f -printf "%T@ %s %p\n" | sort -n > /tmp/after.txt
diff /tmp/before.txt /tmp/after.txt | head -40
```

**기대**: vpub-exporter 가 만든 변경 0건 (publisher 자체 갱신은 expected diff).

### QS-5.2 — lsof 로 fopen 모드 확인

```bash
pid=$(systemctl show -p MainPID --value vpub-exporter)
sudo lsof -p $pid 2>/dev/null | awk 'NR==1 || /v-publisher|validator-publisher/'
# FD 열 의 마지막 글자: r = readonly, w/u = write/append (있으면 X)
sudo lsof -p $pid 2>/dev/null | awk 'NR>1 && $4 ~ /[wu]$/ && /v-publisher|validator-publisher/'
# 위 결과가 비어 있어야 합격
```

**기대**: write fd 0건.

### QS-5.3 — strace 로 open(2) 모드 확인 (선택)

```bash
pid=$(systemctl show -p MainPID --value vpub-exporter)
sudo timeout 30 strace -f -e openat -p $pid 2>&1 \
  | grep -E "v-publisher|validator-publisher" \
  | grep -vE "O_RDONLY|O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK"
# 결과가 비어 있어야 publisher 디렉토리에 write/create 호출 0건
```

**합격**: QS-5.1 diff 비어 있음 + QS-5.2 write fd 0 + QS-5.3 비-RDONLY 호출 0.

---

## QS-6 — Constitution 회귀

`make constitution-gate` (Makefile) 가 본 항목들을 자동화 — CI / pre-commit 시 회귀 방지. 결과 (2026-05-24 기준):

- [x] `vpub_service_up`, `vpub_child_count`, `vpub_component_log_mtime_seconds` 가 `/metrics` 에 정상 노출 — testnet 가동 실측 OK (Tier 0)
- [x] 시크릿 leak 0 hit — `make secrets-leak` (3 tests: NoNeedleLeaks / NoGenericSecretPatterns / NoEmbeddedSecrets) 통과
- [x] `alertLevel` 라벨 ∈ {critical, high, medium, low, disk} — `make constitution-gate § V` 자동 검사
- [x] `promtool check rules` 통과 — 26 rules (Tier 0=7 + Tier 1=16 + Tier 2=3)
- [x] systemd `ProtectSystem=full` + `ReadOnlyPaths=/home/admin/v-publisher` (시작 시 dbus 인증), `PrivateTmp=no` (publisher /tmp 격리 차단 — R-014)
- [x] HTTP timeout: Slack 5s / RPC 5s / Binary HEAD 10s — `make constitution-gate § VI` grep 자동 검사
