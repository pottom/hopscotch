// Package updater checks for and applies new hopscotch releases from GitHub.
package updater

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	repo    = "pottom/hopscotch"
	apiURL  = "https://api.github.com/repos/" + repo + "/releases/latest"
	timeout = 10 * time.Second
)

// Release holds the relevant fields from the GitHub releases API.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset is a single downloadable file attached to a release.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// LatestRelease fetches the latest release tag and download URL for the
// current platform from the GitHub API.
func LatestRelease() (*Release, error) {
	client := &http.Client{Timeout: timeout}
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s", resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("parsing release info: %w", err)
	}
	return &rel, nil
}

// AssetURL returns the download URL for the current OS/arch, or "" if not found.
func (r *Release) AssetURL() string {
	name := assetName()
	for _, a := range r.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func assetName() string {
	name := fmt.Sprintf("hopscotch-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// IsNewer reports whether tag is newer than current (simple string compare
// after stripping the leading "v"; good enough for semver x.y.z).
func IsNewer(current, tag string) bool {
	cur := strings.TrimPrefix(current, "v")
	latest := strings.TrimPrefix(tag, "v")
	return latest > cur
}

// Download fetches url and writes it to dst atomically (temp file + rename).
// It also sets the executable bit.
func Download(url, dst string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	// Write to a temp file in the same directory so rename is atomic.
	tmp, err := os.CreateTemp(dirOf(dst), ".hopscotch-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op if rename succeeded

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("writing download: %w", err)
	}
	tmp.Close()

	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("replacing binary: %w", err)
	}
	// macOS requires ad-hoc signing; cross-compiled binaries have no signature.
	if runtime.GOOS == "darwin" {
		_ = exec.Command("codesign", "--force", "--sign", "-", dst).Run()
	}
	return nil
}

// InContainer reports whether the process appears to be running inside a
// Docker or Kubernetes container. Updates are skipped in that case.
func InContainer() bool {
	// Kubernetes injects this env var.
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	// Docker creates this file.
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// Linux cgroup v1: Docker/k8s show up in /proc/1/cgroup.
	if f, err := os.Open("/proc/1/cgroup"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "docker") ||
				strings.Contains(line, "kubepods") ||
				strings.Contains(line, "containerd") {
				return true
			}
		}
	}
	// Running as PID 1 is a strong container signal.
	if os.Getpid() == 1 {
		return true
	}
	return false
}

// SelfPath returns the absolute path of the running binary.
func SelfPath() (string, error) {
	return os.Executable()
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
