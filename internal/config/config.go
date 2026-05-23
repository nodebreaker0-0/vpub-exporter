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

	// Tier 2
	BinaryPath string
	BinaryURL  string

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
	// R-003 confirmed (2026-05-23 testnet 9.7h, 8084 oracle events / 222 bridge CRIT):
	//   - bridge ok : scanned line w/ votes_sent>0 (cumulative — used ONLY to
	//                 advance bridge_last_vote_success_unix; never increments counter)
	//   - bridge fail: CRIT validator_publisher::bridge_voter::runner: critical error vote failed
	//                  (개별 이벤트 라인)
	//   - disagree (임시): WARN bridge_voter::runner: RPC failed
	//   - oracle  ok : INFO reference_oracle_publisher: oracle action sent
	//   - oracle fail: INFO exchange_client: hyperliquid response status=[45]xx
	//   - outcome WARN/CRIT: outcome-voter 모듈 path 한정 — oracle WARN price drift 제외
	VoteOKPatterns         []string // bridge — last_vote_success_unix 트리거 (counter X)
	VoteFailPatterns       []string // bridge CRIT
	DisagreementPatterns   []string // bridge disagreement (임시)
	OracleVoteOKPatterns   []string // oracle action sent
	OracleVoteFailPatterns []string // oracle 4xx/5xx response
	LogWarnPatterns        []string // outcome-voter 한정
	LogCritPatterns        []string // outcome-voter 한정
}

// Default values. spec.md / research.md 에서 가정된 값들.
// R-001~007 가동 첫날 확정 후 백포트.
const (
	DefaultListenAddr      = ":8002"
	DefaultServiceName     = "v-publisher.service"
	DefaultScrapeInterval  = 30 * time.Second
	DefaultShutdownTimeout = 10 * time.Second
	DefaultVisorLogDir     = "/home/admin/v-publisher/log" // testnet default; mainnet uses /home/ubuntu/...
	DefaultComponentLogDir = "/tmp/validator-publisher"    // visor hard-codes this; both networks
	DefaultBinaryPath      = "/home/admin/v-publisher/visor"
)

// defaultPatterns — R-003 confirmed (2026-05-23 testnet logs).
// Patterns are bound to the real Rust module paths emitted by validator-publisher.
// Mainnet 가동 후 변동 시 env override (VPUB_*_PATTERNS).
var (
	// bridge: trigger for last_vote_success_unix only (counter is intentionally
	// not incremented — votes_sent is cumulative in the scanned line).
	defaultVoteOKPatterns = []string{
		`validator_publisher::bridge_voter::runner: scanned .* votes_sent=[1-9]\d*`,
	}
	// bridge: individual CRIT event per failed deposit vote (testnet 5/22: 222 events).
	defaultVoteFailPatterns = []string{
		`CRIT\s+validator_publisher::bridge_voter::runner: critical error vote failed`,
	}
	// bridge: 임시 — mainnet 에서 진짜 RPC disagreement 라인 관찰 시 재정.
	defaultDisagreementPatterns = []string{
		`WARN\s+validator_publisher::bridge_voter::runner: RPC failed`,
	}
	// oracle: 4.3s 평균 (testnet 8084건).
	defaultOracleVoteOKPatterns = []string{
		`validator_publisher::reference_oracle_publisher: oracle action sent`,
	}
	// oracle: 4xx/5xx response from hyperliquid exchange client.
	defaultOracleVoteFailPatterns = []string{
		`validator_publisher::hyperliquid::exchange_client: hyperliquid response status=[45]\d\d`,
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
	if v := getenv("VPUB_BINARY_PATH"); v != "" {
		cfg.BinaryPath = v
	} else {
		cfg.BinaryPath = DefaultBinaryPath
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
	cfg.DisagreementPatterns = splitOrDefault(getenv("VPUB_DISAGREEMENT_PATTERNS"), defaultDisagreementPatterns)
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
