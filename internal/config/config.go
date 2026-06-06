// Package config parses CLI flags and environment variables for vpub-exporter.
//
// Per constitution IV: no secrets are read from publisher's config.json.
// All secrets enter only through environment variables and are never logged.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// ComponentName 은 publisher 의 4 컴포넌트 식별자다.
type ComponentName string

const (
	ComponentVisor                  ComponentName = "visor"
	ComponentBridgeVoter            ComponentName = "bridge-voter"
	ComponentReferenceOraclePublish ComponentName = "reference-oracle-publisher"
	ComponentOutcomeVoter           ComponentName = "outcome-voter"
)

// AllComponents — 메트릭 라벨 cardinality 고정용 (data-model.md Component).
var AllComponents = []ComponentName{
	ComponentVisor,
	ComponentBridgeVoter,
	ComponentReferenceOraclePublish,
	ComponentOutcomeVoter,
}

// Config 는 exporter 가 부팅 시 한 번 만들고 모든 collector 에 주입한다.
// 시크릿 (SlackBotToken / BridgeRPCURLs) 은 String() / 로그에서 마스킹된다.
type Config struct {
	// Server
	ListenAddr      string
	ScrapeInterval  time.Duration
	ShutdownTimeout time.Duration

	// Tier 0 — R-001 confirmed (2026-05-23 testnet 9.7h):
	//   visor self-log dir  = --log-dir argument (default /home/admin/v-publisher/log)
	//   component log dir   = visor's hard-coded /tmp/validator-publisher (both networks)
	// Mainnet runs as user=ubuntu so visor dir becomes /home/ubuntu/v-publisher/log via env.
	ServiceName     string
	VisorLogDir     string // visor's own log file directory
	ComponentLogDir string // root of bridge-voter / reference-oracle-publisher / outcome-voter subdirs

	// Tier 2 — R-019: per-component binary tracking.
	// BinaryTargets maps ComponentName → local file path. Populated from
	// VPUB_BINARY_TARGETS env, with VPUB_BINARY_PATH (legacy) folded into
	// ComponentVisor for backward compat. Defaults cover all 4 components
	// under /home/admin/v-publisher/.
	BinaryTargets map[ComponentName]string
	// BinaryURL is the visor announce URL (sub-tracked via HTTP HEAD).
	// child binaries are NOT HEAD-tracked — visor self-polls /<child>/active
	// (R-019). child download success/failure is inferred from visor log
	// `downloading new binary` line vs child file mtime.
	BinaryURL string

	// FR-012a — bridge-voter state.json path (read-only).
	// Path pattern: <user-home>/v-publisher/bridge-voter-<chain>-state.json
	// Sits in the SAME directory as publisher's config.json — exporter MUST NOT
	// read config.json (Constitution IV). Only this exact path is opened.
	BridgeStatePath string

	// Tier 1 — Bridge RPC. names + per-name URL (URL 은 시크릿 취급).
	BridgeRPCNames []string
	BridgeRPCURLs  map[string]string

	// Tier 1 — Slack
	SlackBotToken  string
	OutcomeChannel string

	// Tier 1 — 로그 패턴 (env override 가능, FR-020).
	// R-003 / R-013 / R-024 confirmed (2026-06-06 mainnet 2.6h):
	//   - bridge ok : scanned line — capture votes_sent group, counter += N (R-024
	//                 정정: 옛 R-003 의 "counter X" 결정은 cumulative line 가정이라
	//                 부정확. mainnet 실측: scanned 라인 votes_sent=N 은 그 한 scan
	//                 의 vote 수. N 추출 후 Add(N) 이 정확.)
	//   - bridge fail: CRIT validator_publisher::bridge_voter::runner: critical error vote failed
	//                  (개별 이벤트 라인)
	//   - bridge provider fail: WARN bridge_voter::runner: RPC failed ... provider="X" ... status NNN
	//                  (R-013 결론: publisher 는 "disagreement" 단어 절대 안 찍음.
	//                   옛 disagreement_total 메트릭은 사실 provider HTTP error 의
	//                   counter 였음. provider name + status_code 라벨로 의미 명확화.)
	//   - oracle  ok : INFO reference_oracle_publisher: oracle action sent
	//   - oracle fail: CRIT reference_oracle_publisher: critical error failed to publish oracle action
	//                  (R-024 정정: 옛 [45]xx 패턴은 HF response status=200 인데
	//                   data 안에 "Missing price" 거대 array 인 케이스 못 잡음.
	//                   publisher 가 직접 찍는 CRIT 라인이 정확한 fail signal.)
	//   - outcome WARN/CRIT: outcome-voter 모듈 path 한정 — oracle WARN price drift 제외
	VoteOKPatterns         []string // bridge — scanned, votes_sent=(\d+) capture group
	VoteFailPatterns       []string // bridge CRIT
	ProviderFailPatterns   []string // bridge RPC provider HTTP fail (R-013 rename, was DisagreementPatterns)
	OracleVoteOKPatterns   []string // oracle action sent
	OracleVoteFailPatterns []string // oracle CRIT
	LogWarnPatterns        []string // outcome-voter 한정
	LogCritPatterns        []string // outcome-voter 한정
}

// Default values. spec.md / research.md 에서 가정된 값들.
// R-001~007 가동 첫날 확정 후 백포트.
const (
	DefaultListenAddr      = ":8002"
	DefaultServiceName     = "validator-publisher.service"
	DefaultScrapeInterval  = 30 * time.Second
	DefaultShutdownTimeout = 10 * time.Second
	DefaultVisorLogDir     = "/home/admin/v-publisher/log" // testnet default; mainnet uses /home/ubuntu/...
	DefaultComponentLogDir = "/tmp/validator-publisher"    // visor hard-codes this; both networks
	DefaultBinaryPath      = "/home/admin/v-publisher/visor"
	// DefaultBinaryRoot — child binaries colocate with visor (R-019).
	DefaultBinaryRoot = "/home/admin/v-publisher"
)

// defaultBinaryTargets — R-019: all 4 components colocated under
// /home/admin/v-publisher. Mainnet env overrides via VPUB_BINARY_TARGETS.
func defaultBinaryTargets() map[ComponentName]string {
	return map[ComponentName]string{
		ComponentVisor:                  DefaultBinaryRoot + "/visor",
		ComponentBridgeVoter:            DefaultBinaryRoot + "/bridge-voter",
		ComponentOutcomeVoter:           DefaultBinaryRoot + "/outcome-voter",
		ComponentReferenceOraclePublish: DefaultBinaryRoot + "/reference-oracle-publisher",
	}
}

// defaultPatterns — R-003 (testnet 2026-05-23) + R-013/R-024 (mainnet 2026-06-06).
// Patterns are bound to the real Rust module paths emitted by validator-publisher.
// Mainnet 가동 후 변동 시 env override (VPUB_*_PATTERNS).
//
// CAPTURE GROUPS (vote_logs.go classifyBridge depends on this):
//   - VoteOKPatterns         : group 1 = votes_sent (\d+)
//   - ProviderFailPatterns   : group 1 = provider name, group 2 = HTTP status code
//   - 나머지 패턴은 캡쳐 그룹 없음 (boolean match only)
var (
	// bridge ok: scanned 라인. votes_sent 캡쳐 — counter += N (R-024).
	defaultVoteOKPatterns = []string{
		`validator_publisher::bridge_voter::runner: scanned .* votes_sent=(\d+)`,
	}
	// bridge fail: individual CRIT event per failed deposit vote (testnet 5/22: 222 events).
	defaultVoteFailPatterns = []string{
		`CRIT\s+validator_publisher::bridge_voter::runner: critical error vote failed`,
	}
	// bridge provider HTTP fail — capture provider name + status code (R-013).
	// mainnet 2.6h: drpc 500 (188×), chainstack 403 (35×).
	defaultProviderFailPatterns = []string{
		`WARN\s+validator_publisher::bridge_voter::runner: RPC failed.*provider="([^"]+)".*status (\d{3})`,
	}
	// oracle ok: testnet 4.3s 평균, mainnet 3.75s 평균.
	defaultOracleVoteOKPatterns = []string{
		`validator_publisher::reference_oracle_publisher: oracle action sent`,
	}
	// oracle fail: CRIT 라인이 정확한 signal (R-024). 옛 [45]xx 패턴은 HF response
	// status=200 + data "Missing price" 거대 array 케이스 못 잡았음.
	defaultOracleVoteFailPatterns = []string{
		`CRIT\s+validator_publisher::reference_oracle_publisher.*: critical error failed to publish oracle action`,
	}
	// outcome-voter scoped — oracle 의 price drift WARN 라인 제외.
	defaultLogWarnPatterns = []string{
		`WARN\s+validator_publisher::outcome_voter`,
	}
	defaultLogCritPatterns = []string{
		`(CRIT|ERROR)\s+validator_publisher::outcome_voter`,
	}
)

// Load reads flags from `fs` and env from `getenv`. Flag args come from `args`.
// Inject these to make tests deterministic; production calls LoadFromOS().
func Load(args []string, getenv func(string) string) (*Config, error) {
	fs := flag.NewFlagSet("vpub-exporter", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	cfg := &Config{
		BridgeRPCURLs: map[string]string{},
	}

	fs.StringVar(&cfg.ListenAddr, "listen-addr", DefaultListenAddr, "HTTP listen address for /metrics")
	fs.StringVar(&cfg.ServiceName, "service-name", DefaultServiceName, "systemd unit name to monitor")
	fs.DurationVar(&cfg.ScrapeInterval, "scrape-interval", DefaultScrapeInterval, "default collector tick interval")
	fs.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", DefaultShutdownTimeout, "graceful HTTP shutdown timeout")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("flag parse: %w", err)
	}

	if v := getenv("VPUB_VISOR_LOG_DIR"); v != "" {
		cfg.VisorLogDir = v
	} else {
		cfg.VisorLogDir = DefaultVisorLogDir
	}
	if v := getenv("VPUB_COMPONENT_LOG_DIR"); v != "" {
		cfg.ComponentLogDir = v
	} else {
		cfg.ComponentLogDir = DefaultComponentLogDir
	}
	// R-019: per-component binary tracking. Resolution order:
	//   1. start with defaults (all 4 colocated under /home/admin/v-publisher)
	//   2. legacy VPUB_BINARY_PATH overrides visor only (backward compat)
	//   3. VPUB_BINARY_TARGETS (comma-separated component=path) wins last
	cfg.BinaryTargets = defaultBinaryTargets()
	if v := getenv("VPUB_BINARY_PATH"); v != "" {
		cfg.BinaryTargets[ComponentVisor] = v
	}
	if raw := getenv("VPUB_BINARY_TARGETS"); raw != "" {
		for _, pair := range strings.Split(raw, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) != 2 {
				continue
			}
			name := ComponentName(strings.TrimSpace(kv[0]))
			path := strings.TrimSpace(kv[1])
			if name == "" || path == "" {
				continue
			}
			// Only known components — silently drop unknowns (data-model invariant).
			switch name {
			case ComponentVisor, ComponentBridgeVoter, ComponentOutcomeVoter, ComponentReferenceOraclePublish:
				cfg.BinaryTargets[name] = path
			}
		}
	}
	cfg.BinaryURL = getenv("VPUB_BINARY_URL")
	cfg.BridgeStatePath = getenv("VPUB_BRIDGE_STATE_PATH")
	cfg.SlackBotToken = getenv("VPUB_SLACK_BOT_TOKEN")
	cfg.OutcomeChannel = getenv("VPUB_OUTCOME_CHANNEL")

	if names := getenv("VPUB_BRIDGE_RPC_NAMES"); names != "" {
		for _, n := range strings.Split(names, ",") {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			cfg.BridgeRPCNames = append(cfg.BridgeRPCNames, n)
			url := getenv("VPUB_RPC_" + envKey(n) + "_URL")
			cfg.BridgeRPCURLs[n] = url
		}
	}

	cfg.VoteOKPatterns = splitOrDefault(getenv("VPUB_VOTE_OK_PATTERNS"), defaultVoteOKPatterns)
	cfg.VoteFailPatterns = splitOrDefault(getenv("VPUB_VOTE_FAIL_PATTERNS"), defaultVoteFailPatterns)
	// R-013 rename: VPUB_DISAGREEMENT_PATTERNS → VPUB_PROVIDER_FAIL_PATTERNS.
	// 옛 env 도 backward compat 로 읽음 (deprecation; mainnet 가동 직후라 외부 사용자
	// 거의 없을 것).
	cfg.ProviderFailPatterns = splitOrDefault(
		coalesce(getenv("VPUB_PROVIDER_FAIL_PATTERNS"), getenv("VPUB_DISAGREEMENT_PATTERNS")),
		defaultProviderFailPatterns,
	)
	cfg.OracleVoteOKPatterns = splitOrDefault(getenv("VPUB_ORACLE_OK_PATTERNS"), defaultOracleVoteOKPatterns)
	cfg.OracleVoteFailPatterns = splitOrDefault(getenv("VPUB_ORACLE_FAIL_PATTERNS"), defaultOracleVoteFailPatterns)
	cfg.LogWarnPatterns = splitOrDefault(getenv("VPUB_LOG_WARN_PATTERNS"), defaultLogWarnPatterns)
	cfg.LogCritPatterns = splitOrDefault(getenv("VPUB_LOG_CRIT_PATTERNS"), defaultLogCritPatterns)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadFromOS — convenience wrapper for main().
func LoadFromOS() (*Config, error) {
	return Load(os.Args[1:], os.Getenv)
}

// Validate checks core invariants. Tier 1/2 fields are advisory — empty values just disable those collectors.
func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return errors.New("listen-addr is empty")
	}
	if c.ServiceName == "" {
		return errors.New("service-name is empty")
	}
	if c.ScrapeInterval <= 0 {
		return errors.New("scrape-interval must be > 0")
	}
	if c.VisorLogDir == "" {
		return errors.New("VPUB_VISOR_LOG_DIR (or default) is empty")
	}
	if c.ComponentLogDir == "" {
		return errors.New("VPUB_COMPONENT_LOG_DIR (or default) is empty")
	}
	for _, n := range c.BridgeRPCNames {
		if c.BridgeRPCURLs[n] == "" {
			return fmt.Errorf("VPUB_RPC_%s_URL is empty", envKey(n))
		}
	}
	return nil
}

// HasBridgeRPC — Tier 1 collector activation hint.
func (c *Config) HasBridgeRPC() bool { return len(c.BridgeRPCNames) > 0 }

// HasSlack — Tier 1 collector activation hint.
func (c *Config) HasSlack() bool { return c.SlackBotToken != "" }

// HasBinaryRemote — Tier 2 collector activation hint.
func (c *Config) HasBinaryRemote() bool { return c.BinaryURL != "" }

// HasBridgeState — FR-012a collector activation hint.
func (c *Config) HasBridgeState() bool { return c.BridgeStatePath != "" }

func envKey(name string) string {
	r := strings.ToUpper(name)
	r = strings.ReplaceAll(r, "-", "_")
	r = strings.ReplaceAll(r, ".", "_")
	return r
}

// coalesce returns the first non-empty argument, or "" if all are empty.
func coalesce(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitOrDefault(raw string, def []string) []string {
	if raw == "" {
		return def
	}
	var out []string
	for _, p := range strings.Split(raw, "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return def
	}
	return out
}
