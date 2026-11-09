package bootstrap

import (
	"testing"

	"github.com/Ricaardo/nimbus-os/news/internal/config"
)

func TestBuildEventEngineOffReturnsNil(t *testing.T) {
	engine, err := buildEventEngine(&config.PlatformConfig{Events: config.EventsConfig{Mode: "off"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if engine != nil {
		t.Fatal("mode=off should not construct an engine")
	}
}

func TestBuildEventEngineShadowUsesDefaults(t *testing.T) {
	cfg := &config.PlatformConfig{Events: config.EventsConfig{
		Mode:       "shadow",
		QuietStart: "23:30",
		QuietEnd:   "07:00",
	}}
	engine, err := buildEventEngine(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if engine == nil || engine.Mode() != "shadow" {
		t.Fatalf("engine = %+v", engine)
	}
	if engine.Active() {
		t.Fatal("shadow must not be active")
	}
}
