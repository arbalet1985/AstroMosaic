package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionCrashLog(t *testing.T) {
	if mode := os.Getenv("ASTROMOSAIC_CRASH_TEST"); mode != "" {
		if err := initSessionLog(); err != nil {
			t.Fatal(err)
		}
		u := &ui{status: "test operation before crash"}
		previous := ""
		logUIStatus(u, &previous)
		if mode == "worker" {
			go func() { panic("diagnostic worker panic") }()
			select {}
		}
		panic("diagnostic main panic")
	}
	for _, mode := range []string{"main", "worker"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSessionCrashLog$")
			cmd.Env = append(os.Environ(), "ASTROMOSAIC_CRASH_TEST="+mode, "LOCALAPPDATA="+dir, "XDG_CACHE_HOME="+dir)
			// The GUI has no usable stderr. The log must survive without it.
			cmd.Stderr = io.Discard
			if err := cmd.Run(); err == nil {
				t.Fatal("child should crash")
			}
			files, err := filepath.Glob(filepath.Join(dir, "AstroMosaic", "logs", "*.log"))
			if err != nil || len(files) != 1 {
				t.Fatalf("expected one session log: %v, %v", files, err)
			}
			data, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"SESSION START", "test operation before crash", "heap=", "panic: diagnostic " + mode + " panic", "goroutine ", "crashlog_test.go:"} {
				if !strings.Contains(string(data), want) {
					t.Errorf("log missing %q: %s", want, data)
				}
			}
		})
	}
}

func TestOpenSessionLogFallback(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, nil, 0600); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(dir, "fallback")
	f, err := openSessionLog([]string{filepath.Join(blocked, "logs"), fallback})
	if err != nil {
		t.Fatal(err)
	}
	first := f.Name()
	f.Close()
	f, err = openSessionLog([]string{fallback})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if filepath.Dir(first) != fallback || first == f.Name() {
		t.Fatal("fallback must create a fresh file without overwriting earlier logs")
	}
	if _, err := openSessionLog([]string{blocked}); err == nil {
		t.Fatal("unwritable directories must report an error")
	}
}
