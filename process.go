package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// defaultRepo is the GitHub owner/repo of the patched AltServer-Linux fork
// that fixes the September 2026 Apple sign-in block and the iOS 26.4+
// code-signing crash. It can be overridden in the UI if the fork moves or
// a newer one appears — check the repo's GitHub Releases page if this
// stops resolving.
const defaultRepo = "jaakkopalvaila/AltServer-Linux"

const anisetteImage = "dadoum/anisette-v3-server"
const anisetteContainerName = "anisette-v3"
const anisetteAddr = "127.0.0.1:6969"
const anisetteURL = "http://" + anisetteAddr

// logFunc receives one line of output at a time, in order.
type logFunc func(string)

// ---------------------------------------------------------------------
// Process streaming helper
// ---------------------------------------------------------------------

// runStreamed runs a command, streaming combined stdout/stderr line-by-line
// to onLine, and returns once the process exits.
func runStreamed(ctx context.Context, env []string, onLine logFunc, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanLines(stdout, onLine)
	}()
	go func() {
		defer wg.Done()
		scanLines(stderr, onLine)
	}()
	wg.Wait()

	return cmd.Wait()
}

func scanLines(r io.Reader, onLine logFunc) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if onLine != nil {
			onLine(scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil && onLine != nil {
		onLine("Stream read error: " + err.Error())
	}
}

// ---------------------------------------------------------------------
// Docker / anisette
// ---------------------------------------------------------------------

func checkDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found in PATH — install Docker first")
	}
	return nil
}

func isAnisetteRunning() bool {
	conn, err := net.DialTimeout("tcp", anisetteAddr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func checkAnisetteHealth() (bool, string) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(anisetteURL)
	if err != nil {
		return false, "Connection error: " + err.Error()
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	detail := strings.TrimSpace(string(body))
	if len(detail) > 40 {
		detail = detail[:40] + "..."
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		if detail != "" {
			return true, fmt.Sprintf("HTTP %d OK (%s)", resp.StatusCode, detail)
		}
		return true, fmt.Sprintf("HTTP %d OK", resp.StatusCode)
	}
	return false, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, detail)
}

func getAnisetteContainerStatus() string {
	out, err := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", anisetteContainerName).Output()
	if err != nil {
		return "not created"
	}
	return strings.TrimSpace(string(out))
}

func isAnisettePortMapped() bool {
	out, err := exec.Command("docker", "port", anisetteContainerName, "6969").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func startAnisette(onLine logFunc) error {
	if err := checkDocker(); err != nil {
		return err
	}

	cStatus := getAnisetteContainerStatus()

	// If container exists, check whether port 6969 is mapped. If not, recreate.
	if cStatus != "not created" {
		if !isAnisettePortMapped() {
			onLine(fmt.Sprintf("Container '%s' exists but port 6969 is not mapped. Recreating...", anisetteContainerName))
			_ = exec.Command("docker", "rm", "-f", anisetteContainerName).Run()
			cStatus = "not created"
		} else if cStatus == "running" {
			healthy, detail := checkAnisetteHealth()
			if healthy {
				onLine(fmt.Sprintf("Container '%s' is already running and healthy (%s)", anisetteContainerName, detail))
				return nil
			}
			onLine(fmt.Sprintf("Container '%s' is running but healthcheck failed (%s). Restarting...", anisetteContainerName, detail))
			_ = exec.Command("docker", "restart", anisetteContainerName).Run()
		} else {
			onLine("Starting existing container '" + anisetteContainerName + "'...")
			if err := runStreamed(context.Background(), nil, onLine, "docker", "start", anisetteContainerName); err != nil {
				onLine("Failed to start existing container, recreating: " + err.Error())
				_ = exec.Command("docker", "rm", "-f", anisetteContainerName).Run()
				cStatus = "not created"
			}
		}
	}

	if cStatus == "not created" {
		onLine("Initializing container: " + anisetteContainerName + " with -p 6969:6969 ...")
		_ = exec.Command("docker", "rm", "-f", anisetteContainerName).Run()
		err := runStreamed(context.Background(), nil, onLine,
			"docker", "run", "-d",
			"--restart", "always",
			"--name", anisetteContainerName,
			"-p", "6969:6969",
			"--volume", "anisette-v3_data:/home/Alcoholic/.config/anisette-v3/lib/",
			anisetteImage,
		)
		if err != nil {
			return fmt.Errorf("docker run failed: %w", err)
		}
	}

	if !isAnisettePortMapped() {
		return fmt.Errorf("container started but port 6969 is not mapped — check 'docker ps'")
	}

	onLine("Verifying healthcheck on " + anisetteURL + " ...")
	for i := 1; i <= 15; i++ {
		time.Sleep(1 * time.Second)
		healthy, detail := checkAnisetteHealth()
		if healthy {
			onLine(fmt.Sprintf("Healthcheck PASSED: %s", detail))
			return nil
		}
		onLine(fmt.Sprintf("Waiting for server ready (%d/15): %s", i, detail))
	}

	return fmt.Errorf("healthcheck timed out on %s — check 'docker logs %s'", anisetteAddr, anisetteContainerName)
}

func stopAnisette(onLine logFunc) error {
	if err := checkDocker(); err != nil {
		return err
	}
	onLine("Stopping container " + anisetteContainerName + "...")
	return runStreamed(context.Background(), nil, onLine, "docker", "stop", anisetteContainerName)
}

// ---------------------------------------------------------------------
// AltServer-Linux binary: download / arch detection
// ---------------------------------------------------------------------

func archAssetHint() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "aarch64", nil
	case "arm":
		return "armv7", nil
	case "386":
		return "i586", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q — you'll need to build AltServer-Linux from source for this machine", runtime.GOARCH)
	}
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

func fetchLatestRelease(repo string) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d for %s: %s", resp.StatusCode, repo, strings.TrimSpace(string(body)))
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decoding GitHub API response: %w", err)
	}
	return &rel, nil
}

func downloadFile(url, dest string, onLine logFunc) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmp := dest + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, resp.Body)
	_ = out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	if onLine != nil {
		onLine(fmt.Sprintf("Wrote %d bytes to %s", written, dest))
	}
	return nil
}

// downloadAltServer fetches the latest release of the given fork repo and
// saves the asset matching this machine's architecture to destPath.
func downloadAltServer(repo, destPath string, onLine logFunc) error {
	hint, err := archAssetHint()
	if err != nil {
		return err
	}

	onLine("Looking up latest release of " + repo + " ...")
	rel, err := fetchLatestRelease(repo)
	if err != nil {
		return fmt.Errorf("could not query GitHub releases (the fork may have moved, or may not publish prebuilt binaries — try 'Browse for existing binary' and grab one from the repo's Releases page manually instead): %w", err)
	}

	var assetURL, assetName string
	for _, a := range rel.Assets {
		if strings.Contains(strings.ToLower(a.Name), hint) {
			assetURL = a.BrowserDownloadURL
			assetName = a.Name
			break
		}
	}
	if assetURL == "" {
		return fmt.Errorf("release %s has no asset matching %q — check %s's Releases page manually and use 'Browse for existing binary' instead", rel.TagName, hint, repo)
	}

	onLine(fmt.Sprintf("Downloading %s (release %s) ...", assetName, rel.TagName))
	if err := downloadFile(assetURL, destPath, onLine); err != nil {
		return fmt.Errorf("downloading asset: %w", err)
	}
	if err := os.Chmod(destPath, 0o755); err != nil {
		return fmt.Errorf("chmod +x: %w", err)
	}
	onLine("Ready: " + destPath)
	return nil
}

// ---------------------------------------------------------------------
// Device detection (via idevice_id from libimobiledevice-utils)
// ---------------------------------------------------------------------

func listUDIDs() ([]string, error) {
	if _, err := exec.LookPath("idevice_id"); err != nil {
		return nil, fmt.Errorf("idevice_id not found — install libimobiledevice-utils")
	}
	out, err := exec.Command("idevice_id", "-l").Output()
	if err != nil {
		return nil, fmt.Errorf("idevice_id -l failed (is usbmuxd running and the phone plugged in and unlocked?): %w", err)
	}
	var udids []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			udids = append(udids, line)
		}
	}
	return udids, nil
}

// ---------------------------------------------------------------------
// Install
// ---------------------------------------------------------------------

type installParams struct {
	BinPath     string
	UDID        string
	AppleID     string
	Password    string
	IPAPath     string
	WifiMode    bool
	NetmuxdAddr string // e.g. 127.0.0.1:27015, only used when WifiMode is true
}

type prompt2FAFunc func() (string, error)

func runStreamedInteractive(ctx context.Context, env []string, onLine logFunc, prompt2FA prompt2FAFunc, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}

	var asking2FAMu sync.Mutex
	isAsking2FA := false

	is2FAPrompt := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		// Ignore response or status messages (e.g. "Received 2FA response status code: 200")
		if strings.Contains(lower, "response") || strings.Contains(lower, "status") {
			return false
		}
		return strings.HasPrefix(lower, "enter two factor") ||
			strings.HasPrefix(lower, "enter two-factor") ||
			strings.Contains(lower, "enter two factor code") ||
			strings.Contains(lower, "enter two-factor code") ||
			strings.Contains(lower, "enter 2fa code")
	}

	trigger2FA := func(text string) {
		if !is2FAPrompt(text) {
			return
		}

		asking2FAMu.Lock()
		if isAsking2FA || prompt2FA == nil {
			asking2FAMu.Unlock()
			return
		}
		isAsking2FA = true
		asking2FAMu.Unlock()

		defer func() {
			asking2FAMu.Lock()
			isAsking2FA = false
			asking2FAMu.Unlock()
		}()

		if onLine != nil {
			onLine("Two-factor authentication requested. Waiting for code from modal...")
		}

		code, pErr := prompt2FA()
		if pErr == nil && code != "" {
			if onLine != nil {
				onLine("Sending 2FA code to AltServer...")
			}
			_, _ = stdinPipe.Write([]byte(code + "\n"))
		} else {
			if onLine != nil {
				onLine("2FA entry cancelled.")
			}
			_, _ = stdinPipe.Write([]byte("\n"))
		}
	}

	handleOutput := func(r io.Reader) {
		buf := make([]byte, 1024)
		var lineBuf strings.Builder

		for {
			n, err := r.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				for _, ch := range chunk {
					if ch == '\n' || ch == '\r' {
						line := strings.TrimSpace(lineBuf.String())
						lineBuf.Reset()
						if line != "" {
							if onLine != nil {
								onLine(line)
							}
							trigger2FA(line)
						}
					} else {
						lineBuf.WriteRune(ch)
					}
				}

				cur := strings.TrimSpace(lineBuf.String())
				if cur != "" && is2FAPrompt(cur) {
					lineBuf.Reset()
					trigger2FA(cur)
				}
			}
			if err != nil {
				break
			}
		}
		rem := strings.TrimSpace(lineBuf.String())
		if rem != "" {
			if onLine != nil {
				onLine(rem)
			}
			if is2FAPrompt(rem) {
				trigger2FA(rem)
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		handleOutput(stdoutPipe)
	}()
	go func() {
		defer wg.Done()
		handleOutput(stderrPipe)
	}()
	wg.Wait()

	return cmd.Wait()
}

func installApp(p installParams, onLine logFunc, prompt2FA prompt2FAFunc) error {
	if _, err := os.Stat(p.BinPath); err != nil {
		return fmt.Errorf("AltServer binary not found at %s — download or select it first", p.BinPath)
	}
	if _, err := os.Stat(p.IPAPath); err != nil {
		return fmt.Errorf("IPA file not found at %s", p.IPAPath)
	}

	env := append(os.Environ(), "ALTSERVER_ANISETTE_SERVER="+anisetteURL)

	if p.WifiMode {
		addr := strings.TrimSpace(p.NetmuxdAddr)
		if addr == "" {
			addr = "127.0.0.1:27015"
		}
		env = append(env, "USBMUXD_SOCKET_ADDRESS="+addr)
		onLine("Wi-Fi mode: pointing at netmuxd on " + addr + " — make sure netmuxd is actually running there, or this will fail with 'could not find the device'.")
	} else {
		onLine("USB mode: using the system's default usbmuxd socket (no USBMUXD_SOCKET_ADDRESS override).")
	}

	args := []string{"-d", "-u", p.UDID, "-a", p.AppleID, "-p", p.Password, p.IPAPath}
	onLine(fmt.Sprintf("Running: %s -d -u %s -a <apple-id> -p <hidden> %s", p.BinPath, p.UDID, p.IPAPath))

	return runStreamedInteractive(context.Background(), env, onLine, prompt2FA, p.BinPath, args...)
}
