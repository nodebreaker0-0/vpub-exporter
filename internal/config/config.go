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

	// Tier 1 — Bridge RPC. names + per-name URL (URL 은 시크릿 취급).
	BridgeRPCNames []string
	BridgeRPCURLs  map[string]string

	// Tier 1 — Slack
	SlackBotToken  string
	OutcomeChannel string

	// Tier 1 — 로그 패턴 (env override 가능, FR-020)
	VoteOKPatterns       []string
	VoteFailPatterns     []string
	DisagreementPatterns []string
	LogWarnPatterns      []string
	LogCritPatterns      []string
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

// defaultPatterns — research.md R-003 가동 첫날 실측 후 정정.
var (
	defaultVoteOKPatterns       = []string{`(?i)vote.*(submitted|ok|success)`, `(?i)published`}
	defaultVoteFailPatterns     = []string{`(?i)vote.*(fail|error)`}
	defaultDisagreementPatterns = []string{`(?i)disagree`, `(?i)mismatch`, `(?i)consensus failure`}
	defaultLogWarnPatterns      = []string{`(?i)\bwarn\b`, `WARN`}
	defaultLogCritPatterns      = []string{`(?i)\bcrit`, `ERROR`, `FATAL`}
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
