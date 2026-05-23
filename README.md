# vpub-exporter

hl-validator-publisher 외부 모니터링용 Prometheus exporter. publisher 와 같은 머신에 배치, port 8002 에서 `/metrics` 노출.

**Status**: Phase 3 (Tier 0 / MVP) 구현 진행 중. spec 은 `specs/001-vpub-exporter/`.

## 요약

- publisher 의 systemd active / child count / 컴포넌트 로그 mtime 을 외부에서 관찰 (Tier 0).
- Arbitrum RPC 헬스 / vote 로그 패턴 / Slack 토큰 유효성 (Tier 1, 추후).
- 새 publisher 바이너리 announce 감지 (Tier 2, 추후).
- **read-only**: publisher 의 어떤 파일도 수정하지 않음.
- 모든 시크릿은 env 로만 주입, 코드/메트릭/라벨에 노출 금지.

## Build

```sh
make build                  # 로컬 (host arch)
make build-linux            # Linux amd64 (publisher 배포용)
make test                   # 단위 / 통합 테스트
make promtool-check         # alert rule yaml 검증
make verify                 # vet + test + promtool-check
```

## 실행

```sh
./bin/vpub-exporter \
  --listen-addr :8002 \
  --service-name validator-publisher.service \
  --scrape-interval 30s
```

환경변수는 `env/vpub-exporter.env.example` 참조. 운영 환경은 `EnvironmentFile=/etc/vpub-exporter.env` (mode 0600) 권장.

## 운영

- systemd unit 템플릿: `systemd/vpub-exporter.service`
- 자세한 메트릭 / 알람 / 환경변수 명세는 `specs/001-vpub-exporter/` 하위 contracts/ 와 data-model.md 참조.

## 안전성 가이드 (constitution 직결)

- 자동 unjail / vote / restart **금지** — 본 도구는 read-only.
- `config.json` 직접 파싱 **금지** — 필요한 값은 env 로 별도 주입.
- alert_level 은 `critical` / `high` / `medium` / `low` / `disk` 5종에서만 사용.
