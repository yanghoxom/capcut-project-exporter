package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FFmpeg download URL — BtbN's GitHub Actions builds (GPL, win64, latest)
const ffmpegDownloadURL = "https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-gpl.zip"

// appDir returns the directory containing the running executable.
func appDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// FindFFmpeg returns paths to ffmpeg and ffprobe.
// Priority: <appDir>/ffmpeg.exe → PATH
func FindFFmpeg() (ffmpeg, ffprobe string) {
	dir := appDir()

	local := filepath.Join(dir, "ffmpeg.exe")
	localProbe := filepath.Join(dir, "ffprobe.exe")
	if fileExists(local) && fileExists(localProbe) {
		return local, localProbe
	}

	if p, err := exec.LookPath("ffmpeg"); err == nil {
		if pp, err2 := exec.LookPath("ffprobe"); err2 == nil {
			return p, pp
		}
	}

	return "", ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DownloadFFmpeg downloads and extracts ffmpeg.exe + ffprobe.exe next to the
// running executable. progress is called with [0,1] and a status message.
func DownloadFFmpeg(progress func(pct float64, msg string)) error {
	dir := appDir()
	zipPath := filepath.Join(dir, "ffmpeg-download.zip")

	progress(0, "Downloading FFmpeg...")

	resp, err := http.Get(ffmpegDownloadURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	total := resp.ContentLength
	f, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("create zip: %w", err)
	}

	written := int64(0)
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return fmt.Errorf("write zip: %w", werr)
			}
			written += int64(n)
			if total > 0 {
				progress(float64(written)/float64(total)*0.8, fmt.Sprintf("Downloading FFmpeg... %.1f MB", float64(written)/1024/1024))
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return fmt.Errorf("read response: %w", err)
		}
	}
	f.Close()

	progress(0.82, "Extracting ffmpeg.exe and ffprobe.exe...")

	if err := extractFFmpegBinaries(zipPath, dir); err != nil {
		os.Remove(zipPath)
		return err
	}

	os.Remove(zipPath)
	progress(1.0, "FFmpeg ready!")
	return nil
}

// extractFFmpegBinaries pulls only ffmpeg.exe and ffprobe.exe out of the zip.
func extractFFmpegBinaries(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	want := map[string]bool{"ffmpeg.exe": true, "ffprobe.exe": true}
	extracted := 0

	for _, f := range r.File {
		base := filepath.Base(f.Name)
		// Only extract the bin/ entries
		if !want[strings.ToLower(base)] {
			continue
		}
		if !strings.Contains(f.Name, "/bin/") && !strings.Contains(f.Name, `\bin\`) {
			continue
		}

		destPath := filepath.Join(destDir, strings.ToLower(base))
		if err := extractFile(f, destPath); err != nil {
			return fmt.Errorf("extract %s: %w", base, err)
		}
		extracted++
		if extracted == 2 {
			break
		}
	}

	if extracted < 2 {
		return fmt.Errorf("could not find ffmpeg.exe and ffprobe.exe in zip (found %d)", extracted)
	}
	return nil
}

func extractFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
