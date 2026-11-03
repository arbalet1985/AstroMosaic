package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func denoiseExecutable(method string) (string, error) {
	env, name := "ASTROMOSAIC_SIRIL", "siril-cli.exe"
	candidates := []string{filepath.Join(os.Getenv("ProgramFiles"), "Siril", "bin", name)}
	if method == "GraXpert" {
		env, name = "ASTROMOSAIC_GRAXPERT", "GraXpert.exe"
		candidates = []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "GraXpert", name), filepath.Join(os.Getenv("ProgramFiles"), "GraXpert", name)}
	}
	if value := os.Getenv(env); value != "" {
		candidates = append([]string{value}, candidates...)
	}
	if value, err := exec.LookPath(name); err == nil {
		candidates = append(candidates, value)
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s не установлен; укажите путь через %s", method, env)
}

// Optional external engines run in isolated temporary directories. Never modify
// user input files, preferences or model installations. No automatic downloads.
func externalDenoise(m *Img, method string, strength float64) (*Img, error) {
	if strength == 0 {
		out := newImg(m.W, m.H, m.C)
		copy(out.Pix, m.Pix)
		out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
		return out, nil
	}
	exe, err := denoiseExecutable(method)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "astromosaic-denoise-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "input.fits"), filepath.Join(dir, "output.fits")
	if err := writeFITS(input, m); err != nil {
		return nil, err
	}
	var args []string
	if method == "Siril NL-Bayes" {
		script := filepath.Join(dir, "process.ssf")
		text := fmt.Sprintf("requires 1.2.0\nload input.fits\ndenoise -nocosmetic -mod=%.3f\nsave output.fits\n", strength)
		if err := os.WriteFile(script, []byte(text), 0600); err != nil {
			return nil, err
		}
		args = []string{"-d", dir, "-s", script}
	} else {
		// Pin to an already installed model. A missing local model is reported,
		// rather than silently downloading a different model during comparison.
		root := filepath.Join(os.Getenv("LOCALAPPDATA"), "GraXpert", "GraXpert", "denoise-ai-models")
		versions, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("нет локальной модели GraXpert: %w", err)
		}
		version := ""
		for i := len(versions) - 1; i >= 0; i-- {
			if versions[i].IsDir() {
				v := versions[i].Name()
				if _, err := os.Stat(filepath.Join(root, v, "model.onnx")); err == nil {
					version = v
					break
				}
			}
		}
		if version == "" {
			return nil, fmt.Errorf("модель GraXpert model.onnx не найдена")
		}
		args = []string{input, "-cli", "-cmd", "denoising", "-output", strings.TrimSuffix(output, ".fits"), "-strength", fmt.Sprintf("%.3f", strength), "-ai_version", version, "-gpu", "false", "-batch_size", "1"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	log, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(log)
		if len(tail) > 1600 {
			tail = tail[len(tail)-1600:]
		}
		return nil, fmt.Errorf("%s: %w; %s", method, err, tail)
	}
	out, err := loadImage(output)
	if err != nil {
		return nil, fmt.Errorf("%s: нет результата output.fits: %w", method, err)
	}
	if out.W != m.W || out.H != m.H || out.C != m.C {
		return nil, fmt.Errorf("%s изменил размеры/число каналов", method)
	}
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	for i, v := range out.Pix {
		if !finitePoint(float64(v), 0) {
			return nil, fmt.Errorf("%s вернул нечисловые данные", method)
		}
		if m.Pix[i] == 0 || !finitePoint(float64(m.Pix[i]), 0) {
			out.Pix[i] = m.Pix[i]
		}
	}
	return out, nil
}
