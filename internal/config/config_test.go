package config

import (
	"strings"
	"testing"
	"time"
)

func envFromMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(nil, envFromMap(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != DefaultListenAddr {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, DefaultListenAddr)
	}
	if cfg.ServiceName != DefaultServiceName {
		t.Errorf("ServiceName = %q, want %q", cfg.ServiceName, DefaultServiceName)
	}
	if cfg.ScrapeInterval != DefaultScrapeInterval {
		t.Errorf("ScrapeInterval = %v, want %v", cfg.ScrapeInterval, DefaultScrapeInterval)
	}
	if cfg.LogDir != DefaultLogDir {
		t.Errorf("LogDir = %q, want %q", cfg.LogDir, DefaultLogDir)
	}
	if cfg.BinaryPath != DefaultBinaryPath {
		t.Errorf("BinaryPath = %q", cfg.BinaryPath)
	}
	if cfg.HasBridgeRPC() {
		t.Errorf("HasBridgeRPC should be false on defaults")
	}
	if cfg.HasSlack() {
		t.Errorf("HasSlack should be false on defaults")
	}
	if cfg.HasBinaryRemote() {
		t.Errorf("HasBinaryRemote should be false on defaults")
	}
	if len(cfg.VoteOKPatterns) == 0 || len(cfg.LogWarnPatterns) == 0 {
		t.Errorf("default log patterns should be non-empty")
	}
}

func TestLoad_FlagsOverride(t *testing.T) {
	args := []string{
		"--listen-addr", "127.0.0.1:9999",
		"--service-name", "test.service",
		"--scrape-interval", "5s",
	}
	cfg, err := Load(args, envFromMap(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9999" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.ServiceName != "test.service" {
		t.Errorf("ServiceName = %q", cfg.ServiceName)
	}
	if cfg.ScrapeInterval != 5*time.Second {
		t.Errorf("ScrapeInterval = %v", cfg.ScrapeInterval)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	env := map[string]string{
		"VPUB_LOG_DIR":         "/tmp/v-publisher/log",
		"VPUB_BINARY_PATH":     "/usr/local/bin/visor",
		"VPUB_BINARY_URL":      "https://example.com/visor",
		"VPUB_SLACK_BOT_TOKEN": "fake-bot-token",
		"VPUB_OUTCOME_CHANNEL": "C12345",
	}
	cfg, err := Load(nil, envFromMap(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogDir != "/tmp/v-publisher/log" {
		t.Errorf("LogDir = %q", cfg.LogDir)
	}
	if cfg.BinaryPath != "/usr/local/bin/visor" {
		t.Errorf("BinaryPath = %q", cfg.BinaryPath)
	}
	if !cfg.HasBinaryRemote() {
		t.Errorf("HasBinaryRemote should be true")
	}
	if !cfg.HasSlack() {
		t.Errorf("HasSlack should be true")
	}
}

func TestLoad_BridgeRPCList(t *testing.T) {
	env := map[string]string{
		"VPUB_BRIDGE_RPC_NAMES":   "alchemy, infura ,quicknode",
		"VPUB_RPC_ALCHEMY_URL":    "https://eth-mainnet.alchemy.com/v2/k",
		"VPUB_RPC_INFURA_URL":     "https://mainnet.infura.io/v3/k",
		"VPUB_RPC_QUICKNODE_URL":  "https://quicknode.example/",
	}
	cfg, err := Load(nil, envFromMap(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := len(cfg.BridgeRPCNames), 3; got != want {
		t.Fatalf("bridge rpc count = %d, want %d", got, want)
	}
	for _, n := range []string{"alchemy", "infura", "quicknode"} {
		if cfg.BridgeRPCURLs[n] == "" {
			t.Errorf("BridgeRPCURLs[%q] empty", n)
		}
	}
	if !cfg.HasBridgeRPC() {
		t.Errorf("HasBridgeRPC should be true")
	}
}

func TestValidate_BridgeRPCMissingURL(t *testing.T) {
	env := map[string]string{
		"VPUB_BRIDGE_RPC_NAMES": "alchemy",
		// no URL
	}
	_, err := Load(nil, envFromMap(env))
	if err == nil {
		t.Fatalf("expected error for missing RPC URL")
	}
	if !strings.Contains(err.Error(), "VPUB_RPC_ALCHEMY_URL") {
		t.Errorf("error should mention missing URL var, got: %v", err)
	}
}

func TestLoad_PatternsEnvMultiline(t *testing.T) {
	env := map[string]string{
		"VPUB_LOG_WARN_PATTERNS": "pat1\npat2\n  pat3  \n",
	}
	cfg, err := Load(nil, envFromMap(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.LogWarnPatterns; len(got) != 3 || got[0] != "pat1" || got[2] != "pat3" {
		t.Errorf("LogWarnPatterns = %#v", got)
	}
}

func TestValidate_BadScrapeInterval(t *testing.T) {
	args := []string{"--scrape-interval", "0s"}
	_, err := Load(args, envFromMap(nil))
	if err == nil {
		t.Fatalf("expected error for non-positive scrape-interval")
	}
}

func TestEnvKey(t *testing.T) {
	cases := map[string]string{
		"alchemy":     "ALCHEMY",
		"quick-node":  "QUICK_NODE",
		"foo.bar":     "FOO_BAR",
	}
	for in, want := range cases {
		if got := envKey(in); got != want {
			t.Errorf("envKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAllComponents(t *testing.T) {
	if len(AllComponents) != 4 {
		t.Fatalf("AllComponents must have 4 entries (visor + 3 children), got %d", len(AllComponents))
	}
}
