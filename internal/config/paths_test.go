package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicWriteTOML_Permission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.toml")
	data := struct {
		Key string `toml:"key"`
	}{Key: "value"}
	if err := atomicWriteTOML(path, data); err != nil {
		t.Fatalf("atomicWriteTOML: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file perm = %04o, want 0600", perm)
	}
}

func TestAtomicWriteJSON_Permission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")
	data := map[string]string{"key": "value"}
	if err := AtomicWriteJSON(path, data); err != nil {
		t.Fatalf("AtomicWriteJSON: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file perm = %04o, want 0600", perm)
	}
}

func TestInitConfigFile_Permission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file permissions not supported on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "init.toml")
	data := struct {
		Key string `toml:"key"`
	}{Key: "value"}
	if err := initConfigFile(path, data); err != nil {
		t.Fatalf("initConfigFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("file perm = %04o, want 0600", perm)
	}
}
