package backends

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c12s/wormhole/internal/config"
)

func TestStaticAddress(t *testing.T) {
	cases := []struct {
		name, want string
		offset     int
		wantErr    bool
	}{
		{"host-1", "192.168.122.51", 50, false},
		{"node-1", "192.168.122.61", 60, false},
		{"node-3", "192.168.122.63", 60, false},
		{"node", "", 50, true},
		{"node-250", "", 50, true},
	}
	for _, c := range cases {
		got, err := staticAddress(c.name, "192.168.122.", c.offset)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("staticAddress(%q, %d) = %q, %v; want %q, err=%v", c.name, c.offset, got, err, c.want, c.wantErr)
		}
	}
}

func TestDeterministicMACUniquePerAddress(t *testing.T) {
	a := deterministicMAC("192.168.122.51")
	b := deterministicMAC("192.168.122.61")
	if a == b {
		t.Fatalf("distinct addresses share MAC %s", a)
	}
	if a != "52:54:00:a8:7a:33" {
		t.Errorf("unexpected MAC %s", a)
	}
}

func TestNamesDefaultsToAllConfiguredVMs(t *testing.T) {
	l := &LibvirtBackend{conf: &config.Config{NameBase: "node", VMCount: 2}}
	got := l.names(nil)
	if len(got) != 2 || got[0] != "node-1" || got[1] != "node-2" {
		t.Errorf("names(nil) = %v", got)
	}
	if got := l.names([]string{"x-9"}); len(got) != 1 || got[0] != "x-9" {
		t.Errorf("explicit names not kept: %v", got)
	}
}

func TestAddressOwnerDetectsCollision(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "host-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	seed := "version: 1\nconfig:\n  - type: physical\n    subnets:\n      - type: static\n        address: 192.168.122.51\n"
	if err := os.WriteFile(filepath.Join(dir, "host-1", "network-config"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &LibvirtBackend{conf: &config.Config{StateDir: dir}}
	if owner := l.addressOwner("192.168.122.51", "node-1"); owner != "host-1" {
		t.Errorf("collision not detected, owner=%q", owner)
	}
	if owner := l.addressOwner("192.168.122.51", "host-1"); owner != "" {
		t.Errorf("own seed reported as collision")
	}
	if owner := l.addressOwner("192.168.122.61", "node-1"); owner != "" {
		t.Errorf("free address reported taken by %q", owner)
	}
}
