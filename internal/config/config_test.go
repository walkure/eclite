package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := `
bp35a1:
    id      : "0000009A0512368210005525010E3F50"
    pass    : TESTPASSWORD
    device  : /dev/ttyWiSUN
prometheus:
    wdt: 4
    http: 8881
    labels:
        place: home
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BP35A1.ID != "0000009A0512368210005525010E3F50" {
		t.Errorf("BP35A1.ID = %q", cfg.BP35A1.ID)
	}
	if cfg.BP35A1.Device != "/dev/ttyWiSUN" {
		t.Errorf("BP35A1.Device = %q", cfg.BP35A1.Device)
	}
	if cfg.Prometheus.WDT != 4 {
		t.Errorf("Prometheus.WDT = %d, want 4", cfg.Prometheus.WDT)
	}
	if cfg.Prometheus.HTTP != 8881 {
		t.Errorf("Prometheus.HTTP = %d, want 8881", cfg.Prometheus.HTTP)
	}
	if cfg.Prometheus.Labels["place"] != "home" {
		t.Errorf("Prometheus.Labels[place] = %q, want home", cfg.Prometheus.Labels["place"])
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml")); err == nil {
		t.Fatalf("Load: want error for missing file")
	}
}
