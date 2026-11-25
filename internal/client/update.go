package client

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ReleasesURL is where new Windows builds are published (GitHub releases of the app's own repository).
const ReleasesURL = "https://api.github.com/repos/aon082910/household-hub/releases/latest"

// AssetName is the zip with hhub.exe, HouseholdHub.exe and the icons.
const AssetName = "HouseholdHub-windows.zip"

// Release is the newest published version.
type Release struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Notes   string `json:"notes"`
	Page    string `json:"page"`
}

// LatestRelease asks GitHub for the newest release that has a Windows zip.
func LatestRelease() (*Release, error) {
	req, _ := http.NewRequest("GET", ReleasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "HouseholdHub-updater")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var r struct {
		Tag    string `json:"tag_name"`
		Body   string `json:"body"`
		Page   string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, err
	}
	rel := &Release{Version: strings.TrimPrefix(r.Tag, "v"), Notes: r.Body, Page: r.Page}
	for _, a := range r.Assets {
		if a.Name == AssetName {
			rel.URL = a.URL
		}
	}
	if rel.URL == "" {
		return nil, errors.New("the newest release has no Windows download")
	}
	return rel, nil
}

// Newer reports whether version a is newer than b ("1.10.0" > "1.9.2").
func Newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// DownloadUpdate fetches and unpacks a release into %LOCALAPPDATA%\HouseholdHub\update\<version>. Only GitHub
// download hosts are accepted and only the expected files are written.
func DownloadUpdate(rel *Release) (string, error) {
	u, err := url.Parse(rel.URL)
	if err != nil || u.Scheme != "https" || (u.Host != "github.com" && !strings.HasSuffix(u.Host, ".githubusercontent.com")) {
		return "", errors.New("unexpected download address")
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Get(rel.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download answered HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 80<<20))
	if err != nil {
		return "", err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	dir := filepath.Join(Dir(), "update", rel.Version)
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	want := map[string]bool{"hhub.exe": true, "HouseholdHub.exe": true, "icon.ico": true, "icon.png": true}
	got := 0
	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		if !want[name] || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 60<<20))
		rc.Close()
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o755); err != nil {
			return "", err
		}
		got++
	}
	if _, err := os.Stat(filepath.Join(dir, "HouseholdHub.exe")); err != nil || got < 2 {
		return "", errors.New("the download is missing the program files")
	}
	return dir, nil
}

// UpdateScript writes a small .cmd that waits for the app to close, copies the new files over the installed
// ones and starts the app again. The caller runs it and exits.
func UpdateScript(fromDir, installDir string) (string, error) {
	q := func(p string) string { return `"` + p + `"` }
	lines := []string{
		"@echo off",
		"timeout /t 2 /nobreak >nul",
		"taskkill /IM hhub.exe /F >nul 2>&1",
		"taskkill /IM HouseholdHub.exe /F >nul 2>&1",
		"timeout /t 1 /nobreak >nul",
	}
	for _, f := range []string{"hhub.exe", "HouseholdHub.exe", "icon.ico", "icon.png"} {
		lines = append(lines, "if exist "+q(filepath.Join(fromDir, f))+" copy /Y "+q(filepath.Join(fromDir, f))+" "+q(filepath.Join(installDir, f))+" >nul")
	}
	lines = append(lines,
		"start \"\" /D "+q(installDir)+" "+q(filepath.Join(installDir, "hhub.exe"))+" background",
		"start \"\" /D "+q(installDir)+" "+q(filepath.Join(installDir, "HouseholdHub.exe")),
	)
	p := filepath.Join(Dir(), "update", "apply-update.cmd")
	return p, os.WriteFile(p, []byte(strings.Join(lines, "\r\n")+"\r\n"), 0o644)
}
