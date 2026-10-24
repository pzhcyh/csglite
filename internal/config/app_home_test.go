package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppHomeOverrideKeepsConfigInsideIsolatedRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(EnvAppHome, root)
	Reset()
	t.Cleanup(Reset)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StorageDir() != root {
		t.Fatalf("storage %q != %q", cfg.StorageDir(), root)
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ConfigFile)); err != nil {
		t.Fatal(err)
	}
	Reset()
	reloaded, err := Load()
	if err != nil || reloaded.StorageDir() != root {
		t.Fatalf("reload: %+v %v", reloaded, err)
	}
}

func TestAppHomeOverrideRejectsRelativePath(t *testing.T) {
	t.Setenv(EnvAppHome, "relative-home")
	if _, err := AppHome(); err == nil {
		t.Fatal("relative app home accepted")
	}
}

func TestAppHomeDefaultUnchanged(t *testing.T) {
	t.Setenv(EnvAppHome, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := AppHome()
	if err != nil || actual != filepath.Join(home, AppDir) {
		t.Fatalf("unexpected default: %q %v", actual, err)
	}
}
