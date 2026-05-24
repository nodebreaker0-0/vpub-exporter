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
	if cfg.VisorLogDir != DefaultVisorLogDir {
		t.Errorf("VisorLogDir = %q, want %q", cfg.VisorLogDir, DefaultVisorLogDir)
	}
	if cfg.ComponentLogDir != DefaultComponentLogDir {
		t.Errorf("ComponentLogDir = %q, want %q", cfg.ComponentLogDir, DefaultComponentLogDir)
	}
	if got := cfg.BinaryTargets[ComponentVisor]; got != DefaultBinaryPath {
		t.Errorf("BinaryTargets[visor] = %q, want %q", got, DefaultBinaryPath)
	}
	for _, name := range []ComponentName{ComponentVisor, ComponentBridgeVoter, ComponentOutcomeVoter, ComponentReferenceOraclePublish} {
		if cfg.BinaryTargets[name] == "" {
			t.Errorf("BinaryTargets[%s] missing default", name)
		}
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
		"VPUB_VISOR_LOG_DIR":     "/home/ubuntu/v-publisher/log",
		"VPUB_COMPONENT_LOG_DIR": "/var/tmp/validator-publisher",
		"VPUB_BINARY_PATH":       "/usr/local/bin/visor",
		"VPUB_BINARY_URL":        "https://example.com/visor",
		"VPUB_SLACK_BOT_TOKEN":   "fake-bot-token",
		"VPUB_OUTCOME_CHANNEL":   "C12345",
	}
	cfg, err := Load(nil, envFromMap(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.VisorLogDir != "/home/ubuntu/v-publisher/log" {
		t.Errorf("VisorLogDir = %q", cfg.VisorLogDir)
	}
	if cfg.ComponentLogDir != "/var/tmp/validator-publisher" {
		t.Errorf("ComponentLogDir = %q", cfg.ComponentLogDir)
	}
	// Legacy VPUB_BINARY_PATH still folds into visor target (backward compat).
	if got := cfg.BinaryTargets[ComponentVisor]; got != "/usr/local/bin/visor" {
		t.Errorf("BinaryTargets[visor] = %q, want legacy override", got)
	}
	if !cfg.HasBinaryRemote() {
		t.Errorf("HasBinaryRemote should be true")
	}
	if !cfg.HasSlack() {
		t.Errorf("HasSlack should be true")
	}
}

func TestLoad_BinaryTargetsEnv(t *testing.T) {
	// R-019: VPUB_BINARY_TARGETS overrides all 4 component paths in one shot.
	env := map[string]string{
		"VPUB_BINARY_TARGETS": "visor=/m/visor, bridge-voter=/m/bv, outcome-voter=/m/ov, reference-oracle-publisher=/m/rop",
	}
	cfg, err := Load(nil, envFromMap(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[ComponentName]string{
		ComponentVisor:                  "/m/visor",
		ComponentBridgeVoter:            "/m/bv",
		ComponentOutcomeVoter:           "/m/ov",
		ComponentReferenceOraclePublish: "/m/rop",
	}
	for n, p := range want {
		if got := cfg.BinaryTargets[n]; got != p {
			t.Errorf("BinaryTargets[%s] = %q, want %q", n, got, p)
		}
	}
}

func TestLoad_BinaryTargetsRejectsUnknownComponent(t *testing.T) {
	// Cardinality guard: unknown component names must be silently dropped
	// (not added to BinaryTargets). Defaults remain intact.
	env := map[string]string{
		"VPUB_BINARY_TARGETS": "frobnicator=/no/such, visor=/v",
	}
	cfg, err := Load(nil, envFromMap(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.BinaryTargets["frobnicator"]; ok {
		t.Errorf("unknown component should be dropped, got %q", cfg.BinaryTargets["frobnicator"])
	}
	if cfg.BinaryTargets[ComponentVisor] != "/v" {
		t.Errorf("known component should still apply, got %q", cfg.BinaryTargets[ComponentVisor])
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
