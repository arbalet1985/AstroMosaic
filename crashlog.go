package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"
)

// Keep the file open for the entire process, including runtime fatal errors.
var sessionLog *os.File

func openSessionLog(dirs []string) (*os.File, error) {
	var lastErr error
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			lastErr = err
			continue
		}
		f, err := os.CreateTemp(dir, "astro_mosaic-"+time.Now().Format("20060102-150405")+"-*.log")
		if err == nil {
			return f, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("cannot create diagnostic log: %w", lastErr)
}

func initSessionLog() error {
	var dirs []string
	if cache, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(cache, "AstroMosaic", "logs"))
	}
	dirs = append(dirs, filepath.Join(os.TempDir(), "AstroMosaic", "logs"))
	f, err := openSessionLog(dirs)
	if err != nil {
		return err
	}
	// This captures unhandled panics in EVERY goroutine, and runtime fatal
	// errors (including Go out-of-memory), without recovering a broken process.
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		f.Close()
		return fmt.Errorf("configure crash output: %w", err)
	}
	sessionLog = f
	debug.SetTraceback("all")
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("SESSION START pid=%d go=%s platform=%s/%s CPUs=%d log=%s", os.Getpid(), runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), f.Name())
	if exe, err := os.Executable(); err == nil {
		log.Printf("executable=%s", exe)
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		log.Printf("build=%s", info.String())
	}
	return nil
}

func logUIStatus(u *ui, previous *string) {
	if u.status == *previous {
		return
	}
	*previous = u.status
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	log.Printf("STATUS %s | heap=%d MiB sys=%d MiB goroutines=%d", u.status, m.HeapAlloc>>20, m.Sys>>20, runtime.NumGoroutine())
}
