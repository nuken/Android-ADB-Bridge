package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"context"
	"net"
	"net/http"
	"archive/zip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"golang.org/x/mod/semver"
)

//go:embed templates
var embeddedFiles embed.FS

// ==========================================
// 1. Data Structures
// ==========================================
type Tuner struct {
	Name            string `json:"name"`
	DeviceIP        string `json:"device_ip"`
	AdbRoute        string `json:"adb_route,omitempty"`
	DeviceOS        string `json:"device_os,omitempty"` // Added: "android_tv" or "fire_tv"
	Type            string `json:"type"` 
	EncoderURL      string `json:"encoder_url,omitempty"`
	VideoDeviceID   string `json:"video_device_id,omitempty"`
	AudioDeviceID   string `json:"audio_device_id,omitempty"`
	AudioDelayMs    int    `json:"audio_delay_ms,omitempty"`
	DeinterlaceMode string `json:"deinterlace_mode,omitempty"`
	CaptureFormat   string `json:"capture_format,omitempty"` 
	CaptureSize     string `json:"capture_size,omitempty"`   
	CaptureFPS      string `json:"capture_fps,omitempty"`    
	Priority        int    `json:"priority"`
	InUse           bool   `json:"-"`
	ActiveProvider  string `json:"-"`
}

type Provider struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Intent          string `json:"intent,omitempty"` 
	PackageName     string `json:"package_name"`
	Component       string `json:"component"`
	FirePackageName string `json:"fire_package_name,omitempty"` // Added
	FireComponent   string `json:"fire_component,omitempty"`   // Added
	URLTemplate     string `json:"url_template,omitempty"`
	SplashDelayMs   int    `json:"splash_delay_ms,omitempty"`
	PreTuneMacro    string `json:"pre_tune_macro,omitempty"`
	PostTuneMacro   string `json:"post_tune_macro,omitempty"`
	KeepWarm        bool   `json:"keep_warm"`
	WarmupMacro     string `json:"warmup_macro,omitempty"`
}

type Channel struct {
	Name              string `json:"name"`
	ID                string `json:"id"`
	ProviderID        string `json:"provider_id"`
	DeepLinkContentID string `json:"deep_link_content_id,omitempty"`
	TvcGuideStationID string `json:"tvc_guide_stationid,omitempty"`
	TuningMacro       string `json:"tuning_macro,omitempty"`
	TvgShift          string `json:"tvg_shift,omitempty"`
	TvgLogo           string `json:"tvg_logo,omitempty"` 
	GroupTitle        string `json:"group_title,omitempty"` 
}

type AppConfig struct {
	Port          int        `json:"port"`
	EnableLogging bool       `json:"enable_logging"`
	Tuners        []Tuner    `json:"tuners"`
	Providers     []Provider `json:"providers"`
	Channels      []Channel  `json:"channels"`
}

var activeLogFile *os.File

type DShowDevice struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type DeviceList struct {
	Video []DShowDevice `json:"video"`
	Audio []DShowDevice `json:"audio"`
}

type ProbeResult struct {
	Format string `json:"format"`
	Size   string `json:"size"`
	FPS    string `json:"fps"`
}

type GitHubRelease struct {
	TagName string `json:"tag_name"`
	HtmlUrl string `json:"html_url"`
}

var Config AppConfig
var AppVersion = "5.2.1-WIN"
var tunerLock sync.Mutex

var keycodeMap = map[string]string{
	"UP":     "19",
	"DOWN":   "20",
	"LEFT":   "21",
	"RIGHT":  "22",
	"ENTER":  "23",
	"SELECT": "23",
	"OK":     "23",
	"BACK":   "4",
	"HOME":   "3",
	"PLAY":   "85",
	"PAUSE":  "85",
	"STOP":   "86",
	"REV":    "89",
	"FWD":    "90",
	"INFO":   "82",
	"0":      "7",
	"1":      "8",
	"2":      "9",
	"3":      "10",
	"4":      "11",
	"5":      "12",
	"6":      "13",
	"7":      "14",
	"8":      "15",
	"9":      "16",
}

var streamClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableCompression:    true,
	},
	Timeout: 0,
}

// ==========================================
// App Initialization
// ==========================================
func init() {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}

	sharedAdbPath := filepath.Join(programData, "AndroidADBBridge")
	os.MkdirAll(sharedAdbPath, os.ModePerm)

	// Default to completely silent background operation
	log.SetOutput(io.Discard)

	// Automated ADB Key Migration
	userProfile := os.Getenv("USERPROFILE")
	if userProfile != "" {
		oldAdbDir := filepath.Join(userProfile, ".android")
		filesToCopy := []string{"adbkey", "adbkey.pub"}

		for _, file := range filesToCopy {
			oldPath := filepath.Join(oldAdbDir, file)
			newPath := filepath.Join(sharedAdbPath, file)

			// If the key exists in the old path but not the new path, copy it
			if _, err := os.Stat(oldPath); err == nil {
				if _, err := os.Stat(newPath); os.IsNotExist(err) {
					data, err := os.ReadFile(oldPath)
					if err == nil {
						// 0600 permissions match standard RSA key security requirements
						os.WriteFile(newPath, data, 0600)
						log.Printf("Successfully migrated %s to shared vault.\n", file)
					}
				}
			}
		}
	}

	os.Setenv("ANDROID_USER_HOME", sharedAdbPath)
	os.Setenv("ANDROID_SDK_HOME", sharedAdbPath)
}

func setupLogging() {
	if Config.EnableLogging {
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = `C:\ProgramData`
		}
		logFilePath := filepath.Join(programData, "AndroidADBBridge", "tuner_proxy.log")

		// Close any existing handle to release Windows locks
		if activeLogFile != nil {
			activeLogFile.Close()
		}

		// Open with O_TRUNC to wipe previous troubleshooting sessions and keep the file tiny
		file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
		if err == nil {
			activeLogFile = file
			log.SetOutput(activeLogFile)
			log.Println("Debug logging enabled via dashboard.")
		}
	} else {
		log.SetOutput(io.Discard)
		if activeLogFile != nil {
			activeLogFile.Close()
			activeLogFile = nil
		}
	}
}

// ==========================================
// 2. Configuration Management
// ==========================================
func getConfigPath() string {
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = "."
	}
	return filepath.Join(appData, "AndroidADBBridge", "android_channels.json")
}

func getAvailablePort(startPort int) int {
	port := startPort
	for {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return port
		}
		port++
	}
}

func loadConfig() {
	configPath := getConfigPath()
	configDir := filepath.Dir(configPath)
	os.MkdirAll(configDir, os.ModePerm)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		openPort := getAvailablePort(8888)

		Config = AppConfig{
			Port: openPort,
			Providers: []Provider{
				{
					ID:          "yt_tv",
					Name:        "YouTube TV",
					PackageName: "com.google.android.youtube.tvunplugged",
					Component:   "com.google.android.apps.youtube.tvunplugged.activity.MainActivity",
					URLTemplate: "https://tv.youtube.com/watch/{id}",
				},
			},
			Tuners:   []Tuner{},
			Channels: []Channel{},
		}
		saveConfig()
		return
	}

	fileData, _ := os.ReadFile(configPath)
	json.Unmarshal(fileData, &Config)

	for i := range Config.Providers {
		if Config.Providers[i].Intent != "" && Config.Providers[i].PackageName == "" {
			parts := strings.Split(Config.Providers[i].Intent, "/")
			if len(parts) == 2 {
				Config.Providers[i].PackageName = parts[0]
				Config.Providers[i].Component = parts[1]
			} else {
				Config.Providers[i].PackageName = Config.Providers[i].Intent
			}
			Config.Providers[i].Intent = ""
		}
	}
}

func saveConfig() {
	fileData, _ := json.MarshalIndent(Config, "", "  ")
	os.WriteFile(getConfigPath(), fileData, 0644)
}

func apiCheckUpdate(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/nuken/Android-ADB-Bridge/releases/latest", nil)
	req.Header.Set("User-Agent", "Android-ADB-Bridge-Updater")

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, `{"available": false}`, http.StatusOK)
		return
	}
	defer resp.Body.Close()

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		http.Error(w, `{"available": false}`, http.StatusOK)
		return
	}

	// Normalize tags for golang.org/x/mod/semver (must start with 'v')
	remoteVer := release.TagName
	if !strings.HasPrefix(remoteVer, "v") {
		remoteVer = "v" + remoteVer
	}
	
	// Strip "-WIN" from your local AppVersion for comparison
	localVer := "v" + strings.Split(AppVersion, "-")[0]

	// semver.Compare returns 1 if remoteVer > localVer
	updateAvailable := semver.Compare(remoteVer, localVer) > 0

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"available": updateAvailable,
		"version":   release.TagName,
		"url":       release.HtmlUrl,
	})
}

func apiApplyUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Fetch release info
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/nuken/Android-ADB-Bridge/releases/latest", nil)
	req.Header.Set("User-Agent", "Android-ADB-Bridge-Updater")
	
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to reach GitHub", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		http.Error(w, "Failed to parse release", http.StatusInternalServerError)
		return
	}
	
	// 2. Download the installer
	downloadUrl := fmt.Sprintf("https://github.com/nuken/Android-ADB-Bridge/releases/download/%s/AndroidBridge_Setup_%s.exe", release.TagName, release.TagName)
	
	exeResp, err := http.Get(downloadUrl)
	if err != nil || exeResp.StatusCode != 200 {
		http.Error(w, "Failed to download update", http.StatusInternalServerError)
		return
	}
	defer exeResp.Body.Close()

	tempExePath := filepath.Join(os.TempDir(), "AndroidADBBridge_Update.exe")
	out, err := os.Create(tempExePath)
	if err != nil {
		http.Error(w, "Failed to create temp file", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, exeResp.Body); err != nil {
		out.Close()
		http.Error(w, "Failed to save installer", http.StatusInternalServerError)
		return
	}
	out.Close()

	// 3. Launch via PowerShell/CMD wrapper so it waits for setup to finish, then relaunches the app
	installedExe := filepath.Join(getExeDir(), filepath.Base(os.Args[0]))
	launchScript := fmt.Sprintf(`Start-Process -FilePath "%s" -ArgumentList "/VERYSILENT /SUPPRESSMSGBOXES /FORCECLOSEAPPLICATIONS" -Wait; Start-Process -FilePath "%s"`, tempExePath, installedExe)

	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", launchScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	if err := cmd.Start(); err != nil {
		http.Error(w, "Failed to spawn installer wrapper", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
	
	// 4. Terminate the running server to allow Inno Setup to overwrite files
	go func() {
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
}

// ==========================================
// 3. Executable Path Helpers
// ==========================================
func getExeDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exePath)
}

func getAdbPath() string {
	return filepath.Join(getExeDir(), "adb.exe")
}

func getFFmpegPath() string {
	return filepath.Join(getExeDir(), "ffmpeg.exe")
}

// ==========================================
// 4. ADB & Tuning Logic
// ==========================================
func ensureADBReady() {
	adb := getAdbPath()
	log.Println("Verifying ADB daemon availability...")

	// Clean up any stale ADB daemon left locked by a forced Windows Update reboot
	killCmd := exec.Command("taskkill", "/F", "/IM", "adb.exe", "/T")
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	killCmd.Run()
	time.Sleep(500 * time.Millisecond)

	for i := 1; i <= 15; i++ {
		cmd := exec.Command(adb, "start-server")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			log.Println("ADB server initialized successfully.")
			return
		}
		log.Printf("Waiting for ADB daemon to start (attempt %d/15)...\n", i)
		time.Sleep(2 * time.Second)
	}
	log.Println("Warning: ADB server did not respond during startup. Will attempt auto-connects on request.")
}

func adbCommand(deviceIP string, args ...string) (string, error) {
	adb := getAdbPath()

	isUSB := false
	tunerLock.Lock()
	for _, t := range Config.Tuners {
		if t.DeviceIP == deviceIP && t.AdbRoute == "usb" {
			isUSB = true
			break
		}
	}
	tunerLock.Unlock()

	if !isUSB {
		connectCmd := exec.Command(adb, "connect", deviceIP)
		connectCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		connectCmd.Run()
	}

	fullArgs := append([]string{"-s", deviceIP}, args...)
	cmd := exec.Command(adb, fullArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	
	// Detect common ADB dropouts
	droppedOut := err != nil || strings.Contains(outStr, "offline") || strings.Contains(outStr, "device not found")
	
	if !isUSB && droppedOut {
		log.Printf("[%s] ADB connection lost or offline. Auto-reconnecting...\n", deviceIP)
		
		disconnectCmd := exec.Command(adb, "disconnect", deviceIP)
		disconnectCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		disconnectCmd.Run()
		
		connectCmd := exec.Command(adb, "connect", deviceIP)
		connectCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		connectCmd.Run()
		
		// Retry the original command once
		retryCmd := exec.Command(adb, fullArgs...)
		retryCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err = retryCmd.CombinedOutput()
		outStr = strings.TrimSpace(string(out))
	}
	
	if err != nil {
		log.Printf("[%s ADB Error] %v | Output: %s\n", deviceIP, err, outStr)
	}
	
	// Return the string output so we can read Android's response
	return outStr, err
}

func checkADB(t Tuner) bool {
	adb := getAdbPath()
	
	if t.AdbRoute == "usb" {
		cmd := exec.Command(adb, "devices")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return false
		}
		// Look for the specific USB serial in the "device" state
		return strings.Contains(string(out), t.DeviceIP+"\tdevice") || strings.Contains(string(out), t.DeviceIP+" device")
	}

	cmd := exec.Command(adb, "connect", t.DeviceIP)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	outStr := strings.ToLower(string(out))
	return strings.Contains(outStr, "connected")
}

func parseAndExecuteMacro(ctx context.Context, deviceIP string, macroStr string, pkgName string) {
	if strings.TrimSpace(macroStr) == "" {
		return
	}

	tokens := strings.Split(macroStr, ",")
	for _, token := range tokens {

		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}

		parts := strings.SplitN(token, ":", 2)
		action := strings.ToUpper(strings.TrimSpace(parts[0]))
		param := ""
		if len(parts) > 1 {
			param = strings.TrimSpace(parts[1])
		}

		if action == "FORCE_STOP" || action == "KILL" {
			if pkgName != "" {
				log.Printf("[%s Macro] Force stopping package: %s\n", deviceIP, pkgName)
				adbCommand(deviceIP, "shell", "am", "force-stop", pkgName)
			} else {
				log.Printf("[%s Macro] Warning: FORCE_STOP called but no package name provided.\n", deviceIP)
			}
			continue
		}

		if action == "WAIT" || action == "SLEEP" {
			ms := 0
			fmt.Sscanf(param, "%d", &ms)
			if ms > 0 {
				log.Printf("[%s Macro] Waiting %d ms...\n", deviceIP, ms)
				
				// REPLACE time.Sleep with a context-aware wait
				select {
				case <-ctx.Done():
					log.Printf("[%s Macro] Client disconnected mid-tune. Aborting WAIT.\n", deviceIP)
					return
				case <-time.After(time.Duration(ms) * time.Millisecond):
				}
			}
			continue
		}

		keycode, exists := keycodeMap[action]
		if !exists {
			var rawCode int
			if _, err := fmt.Sscanf(action, "%d", &rawCode); err == nil {
				keycode = fmt.Sprintf("%d", rawCode)
				exists = true
			}
		}

		if exists {
			count := 1
			if param != "" {
				fmt.Sscanf(param, "%d", &count)
			}
			if count < 1 {
				count = 1
			}

			log.Printf("[%s Macro] Sending %s (keycode %s) x%d\n", deviceIP, action, keycode, count)
			for c := 0; c < count; c++ {
				// Check for cancellation before pressing a key
				select {
				case <-ctx.Done():
					log.Printf("[%s Macro] Client disconnected mid-tune. Aborting keypress sequence.\n", deviceIP)
					return
				default:
				}

				adbCommand(deviceIP, "shell", "input", "keyevent", keycode)
				
				// REPLACE the 400ms time.Sleep with a context-aware wait
				select {
				case <-ctx.Done():
					return
				case <-time.After(400 * time.Millisecond):
				}
			}
		} else {
			log.Printf("[%s Macro] Warning: Unknown macro action '%s'\n", deviceIP, action)
		}
	}
}

func lockTuner() *Tuner {
	tunerLock.Lock()
	defer tunerLock.Unlock()

	var selectedTuner *Tuner
	selectedIndex := -1
	bestPriority := 999999 // Start with an artificially high number

	// Loop through all tuners and find the available one with the best (lowest) priority number
	for i := range Config.Tuners {
		if !Config.Tuners[i].InUse {
			if Config.Tuners[i].Priority < bestPriority {
				bestPriority = Config.Tuners[i].Priority
				selectedIndex = i
			}
		}
	}

	// If we found a match, lock it and return it
	if selectedIndex != -1 {
		Config.Tuners[selectedIndex].InUse = true
		selectedTuner = &Config.Tuners[selectedIndex]
	}

	return selectedTuner
}

func releaseTuner(deviceIP string) {
	var activeProvID string

	// 1. Grab the active provider, but DO NOT release the tuner yet!
	tunerLock.Lock()
	for i := range Config.Tuners {
		if Config.Tuners[i].DeviceIP == deviceIP {
			activeProvID = Config.Tuners[i].ActiveProvider // Grab the provider ID
			Config.Tuners[i].ActiveProvider = ""           // Reset it
			break // Leave InUse as true for now
		}
	}
	tunerLock.Unlock()

	// 2. Run cleanup in the background so it doesn't block the HTTP response
	go func() {
			if activeProvID != "" {
				var provider *Provider
				for _, p := range Config.Providers {
					if p.ID == activeProvID {
						provider = &p
						break
					}
				}

				if provider != nil && provider.PostTuneMacro != "" {
					log.Printf("[%s] Executing Cleanup (Post-Tune) Macro\n", deviceIP)
					var tunerOS string
					tunerLock.Lock()
					for _, t := range Config.Tuners {
						if t.DeviceIP == deviceIP {
							tunerOS = t.DeviceOS
							break
						}
					}
					tunerLock.Unlock()

					pkg := provider.PackageName
					if tunerOS == "fire_tv" && provider.FirePackageName != "" {
						pkg = provider.FirePackageName
					}
					parseAndExecuteMacro(context.Background(), deviceIP, provider.PostTuneMacro, pkg)
				}
			}

			// 1. ALWAYS send the default Home command to clear the screen
			log.Printf("Released tuner %s. Sending default Home command.\n", deviceIP)
			adbCommand(deviceIP, "shell", "input", "keyevent", "3")
			time.Sleep(1 * time.Second)

			// 2. NEW: Check if any provider is set to Keep Warm, and open it
			for _, prov := range Config.Providers {
				if prov.KeepWarm {
					log.Printf("[%s] Opening default app '%s' to keep it warm...\n", deviceIP, prov.Name)
					
					targetPkg := prov.PackageName
					
					var tunerOS string
					tunerLock.Lock()
					for _, t := range Config.Tuners {
						if t.DeviceIP == deviceIP {
							tunerOS = t.DeviceOS
							break
						}
					}
					tunerLock.Unlock()

					if tunerOS == "fire_tv" && prov.FirePackageName != "" {
						targetPkg = prov.FirePackageName
					}
					
					// Grab the appropriate component based on the OS
targetCmp := prov.Component
if tunerOS == "fire_tv" && prov.FireComponent != "" {
	targetCmp = prov.FireComponent
}

launched := false

// Attempt explicit component launch first (Fixes Leanback / Fire TV issues)
if targetCmp != "" {
    intentStr := fmt.Sprintf("%s/%s", targetPkg, targetCmp)
    
    // REMOVED: "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LEANBACK_LAUNCHER"
    out, err := adbCommand(deviceIP, "shell", "am", "start", "-n", intentStr)
    
    if err == nil && !strings.Contains(strings.ToLower(out), "error") {
        launched = true
    } else {
        log.Printf("[%s] Explicit Keep Warm start blocked/failed for %s. Falling back to monkey...\n", deviceIP, targetCmp)
    }
}

// Fallback to standard launcher if explicit component wasn't provided or failed
if !launched {
	adbCommand(deviceIP, "shell", "monkey", "-p", targetPkg, "-c", "android.intent.category.LAUNCHER", "1")
}
					
					// Hold the tuner lock until the app has finished cold-booting
					sleepMs := prov.SplashDelayMs
					if sleepMs < 4000 {
						sleepMs = 4000 // Guarantee at least a 4-second buffer for cold boots
					}
					log.Printf("[%s] Waiting %dms for the app to initialize before unlocking...\n", deviceIP, sleepMs)
					time.Sleep(time.Duration(sleepMs) * time.Millisecond)

					// NEW: Execute Warmup Macro if one is defined
					if prov.WarmupMacro != "" {
						log.Printf("[%s] Executing Warmup (Idle) Macro\n", deviceIP)
						parseAndExecuteMacro(context.Background(), deviceIP, prov.WarmupMacro, targetPkg)
					}

					break 
				}
			}

			// 3. Release the tuner lock
			tunerLock.Lock()
			for i := range Config.Tuners {
				if Config.Tuners[i].DeviceIP == deviceIP {
					Config.Tuners[i].InUse = false
					break
				}
			}
			tunerLock.Unlock()
		}()
}

func executeTuning(ctx context.Context, deviceIP string, ch Channel) {
	var provider *Provider
	for _, p := range Config.Providers {
		if p.ID == ch.ProviderID {
			provider = &p
			break
		}
	}

	if provider == nil {
		log.Printf("Error: Provider '%s' not found for channel '%s'\n", ch.ProviderID, ch.Name)
		return
	}

	// Look up the specific Tuner's OS
	var tunerOS string
	tunerLock.Lock()
	for _, t := range Config.Tuners {
		if t.DeviceIP == deviceIP {
			tunerOS = t.DeviceOS
			break
		}
	}
	tunerLock.Unlock()

	// Resolve Package & Component based on Tuner OS
	pkg := provider.PackageName
	cmp := provider.Component

	if tunerOS == "fire_tv" {
		if provider.FirePackageName != "" {
			pkg = provider.FirePackageName
		}
		if provider.FireComponent != "" {
			cmp = provider.FireComponent
		}
	}

	tunerLock.Lock()
	for i := range Config.Tuners {
		if Config.Tuners[i].DeviceIP == deviceIP {
			Config.Tuners[i].ActiveProvider = provider.ID
			break
		}
	}
	tunerLock.Unlock()

	log.Printf("Tuning %s to %s via %s (OS: %s | Pkg: %s)\n", deviceIP, ch.Name, provider.Name, tunerOS, pkg)

	// Wake up device
	adbCommand(deviceIP, "shell", "input", "keyevent", "224")
	time.Sleep(500 * time.Millisecond)

	// 1. Pre-Tune Macro
	if provider.PreTuneMacro != "" {
		log.Printf("[%s] Executing Provider Pre-Tune Macro\n", deviceIP)
		parseAndExecuteMacro(ctx, deviceIP, provider.PreTuneMacro, pkg)
	}
	select {
	case <-ctx.Done():
		return
	default:
	}

	// 2. Launch Intent / Deep Link / Macro App
	if provider.URLTemplate != "" && ch.DeepLinkContentID != "" {
		// Deep Link Mode
		targetURL := strings.ReplaceAll(provider.URLTemplate, "{id}", ch.DeepLinkContentID)
		intentStr := fmt.Sprintf("%s/%s", pkg, cmp)
		
		out, _ := adbCommand(deviceIP, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", targetURL, "-n", intentStr)
		
		// Piggyback Check: Did the deep link fail?
		if strings.Contains(strings.ToLower(out), "error") {
			log.Printf("[%s] ERROR: Deep link failed (App missing or crashed). Aborting macros.\n", deviceIP)
			return // Stop execution immediately
		}

	} else if pkg != "" {
        launched := false

        if cmp != "" {
            intentStr := fmt.Sprintf("%s/%s", pkg, cmp)
            
            // REMOVED: "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LEANBACK_LAUNCHER"
            out, err := adbCommand(deviceIP, "shell", "am", "start", "-n", intentStr)
            
            // Check if the explicit launch succeeded without an error output
            if err == nil && !strings.Contains(strings.ToLower(out), "error") {
                launched = true
            } else {
                log.Printf("[%s] Explicit component start blocked/failed for %s. Using fallback launcher...\n", deviceIP, cmp)
            }
        }

		if !launched {
			log.Printf("[%s] Launching package %s via safe launcher fallback...\n", deviceIP, pkg)
			out, _ := adbCommand(deviceIP, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
			
			// Piggyback Check: Did the Monkey command abort?
			if strings.Contains(strings.ToLower(out), "aborted") || strings.Contains(strings.ToLower(out), "error") {
				log.Printf("[%s] ERROR: Safe launcher failed to find app '%s'. Aborting macros.\n", deviceIP, pkg)
				return // Stop execution immediately
			}
		}
	}

	// 3. Splash Delay
	if provider.SplashDelayMs > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(provider.SplashDelayMs) * time.Millisecond):
		}
	}

	// 4. Channel Tuning Macro
	if ch.TuningMacro != "" {
		log.Printf("[%s] Executing Channel Tuning Macro\n", deviceIP)
		parseAndExecuteMacro(ctx, deviceIP, ch.TuningMacro, pkg)
	}
}

// ==========================================
// 5. Hardware Diagnostics & Auto-Detection
// ==========================================
func getEncoderArgs() []string {
	cmd := exec.Command("wmic", "path", "win32_VideoController", "get", "name")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	
	if err == nil {
		outStr := strings.ToUpper(string(out))
		
		if strings.Contains(outStr, "NVIDIA") {
			return []string{"-c:v", "h264_nvenc", "-preset", "p2", "-tune", "ll", "-b:v", "8000k", "-bufsize", "16000k", "-pix_fmt", "yuv420p"}
		}
		if strings.Contains(outStr, "AMD") || strings.Contains(outStr, "RADEON") {
			return []string{"-c:v", "h264_amf", "-usage", "lowlatency", "-b:v", "8000k", "-bufsize", "16000k", "-pix_fmt", "yuv420p"}
		}
		if strings.Contains(outStr, "INTEL") || strings.Contains(outStr, "UHD GRAPHICS") || strings.Contains(outStr, "HD GRAPHICS") {
			return []string{"-c:v", "h264_qsv", "-look_ahead", "1", "-global_quality", "25"} 
		}
	}
	
	return []string{"-c:v", "libx264", "-preset", "veryfast", "-b:v", "8000k", "-bufsize", "16000k", "-pix_fmt", "yuv420p"}
}

func apiProbeDevice(w http.ResponseWriter, r *http.Request) {
	devName := r.URL.Query().Get("name")
	if devName == "" {
		http.Error(w, "Missing device name", http.StatusBadRequest)
		return
	}

	ffmpeg := getFFmpegPath()
	cmd := exec.Command(ffmpeg, "-f", "dshow", "-list_options", "true", "-i", fmt.Sprintf("video=%s", devName))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	
	out, _ := cmd.CombinedOutput()

	re := regexp.MustCompile(`(?:pixel_format|vcodec)=([a-zA-Z0-9_]+).*?max s=([0-9]+x[0-9]+) fps=([0-9.]+)`)
	
	var bestOption ProbeResult
	bestScore := -1

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		matches := re.FindStringSubmatch(line)
		if len(matches) == 4 {
			format := matches[1]
			size := matches[2]
			fps := matches[3]

			score := 0
			if size == "1920x1080" {
				score += 100
			}

			if format == "mjpeg" {
				score += 50
			} else if format == "nv12" {
				score += 40
			} else if format == "yuyv422" || format == "yuy2" {
				score += 30
			}

			if strings.HasPrefix(fps, "60") || strings.HasPrefix(fps, "59") {
				score += 10
			} else if strings.HasPrefix(fps, "50") {
				score += 5
			}

			if score > bestScore {
				bestScore = score
				bestOption = ProbeResult{Format: format, Size: size, FPS: fps}
			}
		}
	}

	if bestOption.Size == "" {
		bestOption = ProbeResult{Format: "mjpeg", Size: "1920x1080", FPS: "60"} 
	}

	bestOption.FPS = strings.TrimSuffix(bestOption.FPS, ".000")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bestOption)
}

func apiUsbDevices(w http.ResponseWriter, r *http.Request) {
	adb := getAdbPath()
	cmd := exec.Command(adb, "devices")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, _ := cmd.CombinedOutput()

	var devices []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of") {
			continue
		}
		parts := strings.Fields(line)
		// Look for standard USB devices (status "device") and skip IP addresses
		if len(parts) == 2 && parts[1] == "device" {
			if !strings.Contains(parts[0], ":") && !strings.Contains(parts[0], ".") {
				devices = append(devices, parts[0])
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devices)
}

func apiDevices(w http.ResponseWriter, r *http.Request) {
	devices := DeviceList{Video: []DShowDevice{}, Audio: []DShowDevice{}}

	ffmpeg := getFFmpegPath()
	cmd := exec.Command(ffmpeg, "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, _ := cmd.CombinedOutput()
	lines := strings.Split(string(out), "\n")
	var currentType string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "DirectShow video devices") {
			currentType = "video"
			continue
		}
		if strings.Contains(line, "DirectShow audio devices") {
			currentType = "audio"
			continue
		}

		if strings.Contains(line, "\"") {
			parts := strings.Split(line, "\"")
			if len(parts) >= 2 {
				val := parts[1]

				if strings.Contains(line, "Alternative name") {
					// Attach the unique hardware path to the last device we just added
					if currentType == "video" && len(devices.Video) > 0 {
						devices.Video[len(devices.Video)-1].ID = val
					} else if currentType == "audio" && len(devices.Audio) > 0 {
						devices.Audio[len(devices.Audio)-1].ID = val
					}
				} else {
					// This is a new primary device name.
					// We temporarily set ID to the friendly name, which acts as a backwards-compatible fallback 
					// just in case FFmpeg doesn't output an Alternative Name for a specific device.
					newDev := DShowDevice{Name: val, ID: val} 

					if strings.Contains(line, "(video)") {
						currentType = "video"
					} else if strings.Contains(line, "(audio)") {
						currentType = "audio"
					}

					if currentType == "video" {
						devices.Video = append(devices.Video, newDev)
					} else if currentType == "audio" {
						devices.Audio = append(devices.Audio, newDev)
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(devices)
}

// ==========================================
// 6. Web Endpoints & Routing
// ==========================================

func apiReleaseTuner(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rawPath := strings.TrimPrefix(r.URL.Path, "/api/release/")
	linkParam := r.URL.Query().Get("link")

	vodKilled := false

	// 1. Release VOD session either by explicit link, by matching tuner IP, or general "/api/release/vod"
	vodSessionsMu.Lock()
	for key, session := range vodSessions {
		shouldKill := false
		if rawPath == "vod" || rawPath == "" {
			shouldKill = true
		} else if rawPath == session.Tuner.DeviceIP {
			shouldKill = true
		} else if linkParam != "" && strings.Contains(key, linkParam) {
			shouldKill = true
		}

		if shouldKill {
			log.Printf("[VOD] Releasing active session '%s' on Tuner %s (%s)\n", key, session.Tuner.Name, session.Tuner.DeviceIP)
			session.Cancel() // Kills FFmpeg / encoder proxy and triggers deferred tuner unlock
			vodKilled = true
		}
	}
	vodSessionsMu.Unlock()

	// 2. If a specific device IP was requested for Live TV, release it normally
	if !vodKilled && rawPath != "" && rawPath != "vod" {
		deviceIP := rawPath
		tunerLock.Lock()
		found := false
		for i := range Config.Tuners {
			if Config.Tuners[i].DeviceIP == deviceIP {
				Config.Tuners[i].InUse = false
				found = true
				log.Printf("Manually force-released tuner %s via API.\n", deviceIP)
				break
			}
		}
		tunerLock.Unlock()

		if found {
			go adbCommand(deviceIP, "shell", "input", "keyevent", "3")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, address := range addrs {
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				ip := ipnet.IP.String()
				if strings.HasPrefix(ip, "192.168.") {
					return ip
				}
				if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "172.") {
					return ip
				}
			}
		}
	}
	return "127.0.0.1"
}

func apiActiveTuners(w http.ResponseWriter, r *http.Request) {
	active := make(map[string]bool)

	tunerLock.Lock()
	for _, t := range Config.Tuners {
		active[t.DeviceIP] = t.InUse
	}
	tunerLock.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(active)
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"status":"ok","version":"%s"}`, AppVersion)))
}

func statusPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(embeddedFiles, "templates/status.html")
	if err != nil {
		http.Error(w, "Could not load template", http.StatusInternalServerError)
		return
	}
	data := map[string]interface{}{"global_settings": map[string]interface{}{"app_version": AppVersion}}
	tmpl.Execute(w, data)
}

func apiConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Config)
	} else if r.Method == "POST" {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &Config)
		saveConfig()
		setupLogging() // Instantly attach or detach the file logger based on the new setting
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "success"}`))
	}
}

func apiDownloadLog(w http.ResponseWriter, r *http.Request) {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	logPath := filepath.Join(programData, "AndroidADBBridge", "tuner_proxy.log")

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		http.Error(w, "Log file not found or logging is disabled.", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", `attachment; filename="tuner_proxy.log"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, logPath)
}

func apiExportConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="android_channels_backup.json"`)
	json.NewEncoder(w).Encode(Config)
}

func apiImportConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("configFile")
	if err != nil {
		http.Error(w, "Error retrieving file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	body, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Error reading file", http.StatusInternalServerError)
		return
	}

	var tempConfig AppConfig
	if err := json.Unmarshal(body, &tempConfig); err != nil {
		http.Error(w, "Invalid JSON configuration", http.StatusBadRequest)
		return
	}

	tunerLock.Lock()
	Config = tempConfig
	saveConfig()
	tunerLock.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
}

func estimateTuningDuration(provider Provider, channel Channel) int {
	ms := 500 // Initial 500ms wake-up delay

	// Helper function to calculate macro time
	calcMacro := func(macroStr string) int {
		macroMs := 0
		if strings.TrimSpace(macroStr) == "" {
			return 0
		}
		tokens := strings.Split(macroStr, ",")
		for _, token := range tokens {
			parts := strings.SplitN(token, ":", 2)
			action := strings.ToUpper(strings.TrimSpace(parts[0]))
			
			if action == "WAIT" || action == "SLEEP" {
				if len(parts) > 1 {
					var waitTime int
					fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &waitTime)
					macroMs += waitTime
				}
			} else {
				count := 1
				if len(parts) > 1 {
					fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &count)
					if count < 1 {
						count = 1
					}
				}
				macroMs += count * 400 // 400ms per simulated keypress
			}
		}
		return macroMs
	}

	ms += calcMacro(provider.PreTuneMacro)
	ms += provider.SplashDelayMs
	ms += calcMacro(channel.TuningMacro)

	// Add a 1.5-second buffer at the end to ensure the UI is fully hidden before un-muting
	return ms + 1500
}

func executeVODTuning(ctx context.Context, deviceIP string, providerID string, deepLink string) {
	var provider *Provider
	for _, p := range Config.Providers {
		if p.ID == providerID {
			provider = &p
			break
		}
	}

	// Look up tuner OS
	var tunerOS string
	tunerLock.Lock()
	for _, t := range Config.Tuners {
		if t.DeviceIP == deviceIP {
			tunerOS = t.DeviceOS
			break
		}
	}
	tunerLock.Unlock()

	pkg := "tv.pluto.android"
	cmp := "com.paramount.android.pplus.features.splash.tv.SplashMediatorActivity"

	if provider != nil {
		if provider.PackageName != "" {
			pkg = provider.PackageName
		}
		// Only override component if it is NOT the default HomeActivity that triggers SecurityException
		if provider.Component != "" && !strings.Contains(provider.Component, "HomeActivity") {
			cmp = provider.Component
		}
		if tunerOS == "fire_tv" {
			if provider.FirePackageName != "" {
				pkg = provider.FirePackageName
			}
			if provider.FireComponent != "" {
				cmp = provider.FireComponent
			}
		}
	}

	// Hard enforce SplashMediatorActivity for Pluto on Android TV
	if pkg == "tv.pluto.android" && tunerOS != "fire_tv" {
		cmp = "com.paramount.android.pplus.features.splash.tv.SplashMediatorActivity"
	}

	tunerLock.Lock()
	for i := range Config.Tuners {
		if Config.Tuners[i].DeviceIP == deviceIP {
			Config.Tuners[i].ActiveProvider = providerID
			break
		}
	}
	tunerLock.Unlock()

	log.Printf("[VOD] Tuning %s via %s (OS: %s | Pkg: %s | Cmp: %s)\n", deviceIP, providerID, tunerOS, pkg, cmp)

	// 1. Wake the device
	wakeOut, _ := adbCommand(deviceIP, "shell", "input", "keyevent", "224")
	log.Printf("[VOD] Wake output: %s\n", wakeOut)
	time.Sleep(500 * time.Millisecond)

	// 2. Force-stop the app to guarantee a clean cold boot for deep link routing
	log.Printf("[VOD] Force-stopping %s for clean boot...\n", pkg)
	stopOut, _ := adbCommand(deviceIP, "shell", "am", "force-stop", pkg)
	log.Printf("[VOD] Force-stop output: %s\n", stopOut)
	
	select {
	case <-ctx.Done():
		return
	case <-time.After(1000 * time.Millisecond):
	}

	// 3. Format the deep link to match Pluto TV's exact internal requirements
	// Input:  https://pluto.tv/en/ondemand/movies/hot-fuzz-2007-1-1/details
	// Output: https://pluto.tv/on-demand/movies/hot-fuzz-2007-1-1
	fixedLink := strings.Replace(deepLink, "/ondemand/", "/on-demand/", 1)
	fixedLink = strings.Replace(fixedLink, "/en/", "/", 1)
	fixedLink = strings.TrimSuffix(fixedLink, "/details")

	log.Printf("[VOD] Firing formatted deep link: %s\n", fixedLink)
	
	// 4. Fire using the explicit component and log the exact result
	intentStr := fmt.Sprintf("%s/%s", pkg, cmp)
	startOut, err := adbCommand(deviceIP, "shell", "am", "start", "-n", intentStr, "-a", "android.intent.action.VIEW", "-d", fixedLink)
	log.Printf("[VOD] am start output: %s | err: %v\n", startOut, err)
}

func streamHandler(w http.ResponseWriter, r *http.Request) {
	channelID := strings.TrimPrefix(r.URL.Path, "/stream/")

	var channel *Channel
	for _, c := range Config.Channels {
		if c.ID == channelID {
			channel = &c
			break
		}
	}

	if channel == nil {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	tuner := lockTuner()
	if tuner == nil {
		http.Error(w, "All tuners are currently in use", http.StatusServiceUnavailable)
		return
	}
	defer releaseTuner(tuner.DeviceIP)

	// Launch tuning asynchronously so the device starts waking and tuning immediately
	go func() {
		executeTuning(r.Context(), tuner.DeviceIP, *channel)
	}()

	var provider *Provider
	for _, p := range Config.Providers {
		if p.ID == channel.ProviderID {
			provider = &p
			break
		}
	}

	blackoutMs := 0
	if provider != nil {
		blackoutMs = estimateTuningDuration(*provider, *channel)
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	// ==========================================
	// 1. LOCAL CAPTURE CARD ROUTE (FFmpeg)
	// ==========================================
	if tuner.Type == "local" {
		ffmpeg := getFFmpegPath()

		captureFormat := tuner.CaptureFormat
		if captureFormat == "" {
			captureFormat = "mjpeg" 
		}
		captureSize := tuner.CaptureSize
		if captureSize == "" {
			captureSize = "1920x1080" 
		}
		captureFPS := tuner.CaptureFPS
		if captureFPS == "" {
			captureFPS = "60" 
		}

		formatFlag := "-vcodec"
		if captureFormat == "nv12" || captureFormat == "yuyv422" || captureFormat == "yuy2" {
			formatFlag = "-pixel_format"
		}

		vfArg := "setpts=PTS-STARTPTS,format=nv12"
		if tuner.DeinterlaceMode == "tff" {
			vfArg = "setpts=PTS-STARTPTS,bwdif=mode=1:parity=0,format=nv12"
		} else if tuner.DeinterlaceMode == "bff" {
			vfArg = "setpts=PTS-STARTPTS,bwdif=mode=1:parity=1,format=nv12"
		}

		if blackoutMs > 0 {
			splashSecs := float64(blackoutMs) / 1000.0
			vfArg += fmt.Sprintf(",drawbox=x=0:y=0:w=iw:h=ih:color=black:t=fill:enable='lte(t,%.2f)'", splashSecs)
		}

		args := []string{
			"-hide_banner", "-loglevel", "error",
		}

		dshowInput := fmt.Sprintf("video=%s", tuner.VideoDeviceID)
		if tuner.AudioDeviceID != "" {
			dshowInput += fmt.Sprintf(":audio=%s", tuner.AudioDeviceID)
		}

		encoderArgs := getEncoderArgs()
		isQSV := false
		for _, a := range encoderArgs {
			if a == "h264_qsv" {
				isQSV = true
				break
			}
		}
		
		if isQSV {
			args = append(args, "-init_hw_device", "qsv=hw", "-filter_hw_device", "hw")
		}

		args = append(args,
			"-rtbufsize", "256M",
			"-thread_queue_size", "1024",
			"-f", "dshow",
			"-video_size", captureSize,
			"-framerate", captureFPS,
			formatFlag, captureFormat, 
			"-i", dshowInput,
			"-vf", vfArg,
		)
		
		args = append(args, encoderArgs...)

		args = append(args,
			"-color_primaries", "bt709",
			"-color_trc", "bt709",
			"-colorspace", "bt709",
			"-color_range", "tv",
		)

		afArg := "asetpts=PTS-STARTPTS"
		if blackoutMs > 0 {
			splashSecs := float64(blackoutMs) / 1000.0
			afArg += fmt.Sprintf(",volume=enable='lte(t,%.2f)':volume=0", splashSecs)
		}

		if tuner.AudioDelayMs > 0 {
			afArg += fmt.Sprintf(",adelay=%d|%d", tuner.AudioDelayMs, tuner.AudioDelayMs)
		}

		afArg += ",aresample=async=1"
		args = append(args, "-af", afArg)

		args = append(args,
			// Video Keyframe Settings (Force 1-second GOP for fast tuning)
			"-g", captureFPS,
			"-keyint_min", captureFPS,
			"-sc_threshold", "0",
			
			// Audio Settings
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000",
			
			// Muxer Settings optimized for Channels DVR fast-start
			"-f", "mpegts",
			"-mpegts_flags", "+resend_headers",
			"-pat_period", "0.1",
			"pipe:1",
		)

		cmd := exec.CommandContext(r.Context(), ffmpeg, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Println("FFmpeg stdout error:", err)
			http.Error(w, "Capture card stdout error", http.StatusInternalServerError)
			return
		}

		if err := cmd.Start(); err != nil {
			log.Println("FFmpeg start error:", err)
			http.Error(w, "Failed to start FFmpeg", http.StatusInternalServerError)
			return
		}

		// Enable chunk flushing for immediate playback delivery
		flusher, canFlush := w.(http.Flusher)

		// Direct stream loop (eliminates goroutine leaks and excess memory allocations)
		buf := make([]byte, 32*1024)
		for {
			// Stop reading if Channels DVR disconnects
			if r.Context().Err() != nil {
				break
			}

			n, err := stdout.Read(buf)
			if n > 0 {
				if _, wErr := w.Write(buf[:n]); wErr != nil {
					break // Client disconnected during write
				}
				if canFlush {
					flusher.Flush()
				}
			}
			
			if err != nil {
				break // FFmpeg closed stdout (EOF or crash)
			}
		}

		// Ensure the process is cleanly killed upon exit
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
		return
	}

	// ==========================================
	// 2. NETWORK ENCODER ROUTE (HTTP Proxy)
	// ==========================================
	
	// Hold off on connecting/streaming until tuning macros finish.
	// This intentionally stalls Channels DVR on the "Tuning..." screen to hide the app boot UI.
	if blackoutMs > 0 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Duration(blackoutMs) * time.Millisecond):
		}
	}

	// Enable chunk flushing for immediate, smooth playback delivery once the stream starts
	flusher, canFlush := w.(http.Flusher)

	req, err := http.NewRequestWithContext(r.Context(), "GET", tuner.EncoderURL, nil)
	if err != nil {
		http.Error(w, "Invalid encoder URL", http.StatusInternalServerError)
		return
	}

	resp, err := streamClient.Do(req)
	if err != nil {
		log.Println("Encoder connection error:", err)
		http.Error(w, "Failed to connect to network encoder", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	buf := make([]byte, 128*1024)
	for {
		if r.Context().Err() != nil {
			break
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				log.Printf("Stream write error: %v\n", wErr)
				break
			}
			// Instantly push the video packet to Channels DVR
			if canFlush {
				flusher.Flush()
			}
		}

		if err != nil {
			if err != io.EOF {
				log.Printf("Stream read error: %v\n", err)
			}
			break
		}
	}
}

// ==========================================
// VOD Session Multiplexer
// ==========================================
type VODSession struct {
	Key         string
	Tuner       *Tuner
	Cancel      context.CancelFunc
	Subscribers map[chan []byte]struct{}
	SubMu       sync.Mutex
	IdleTimer   *time.Timer
	Done        chan struct{}
}

var (
	vodSessions   = make(map[string]*VODSession)
	vodSessionsMu sync.Mutex
)

func (s *VODSession) addSubscriber() chan []byte {
	s.SubMu.Lock()
	defer s.SubMu.Unlock()

	// If an idle teardown timer is running, cancel it immediately
	if s.IdleTimer != nil {
		s.IdleTimer.Stop()
		s.IdleTimer = nil
		log.Printf("[VOD %s] Client reconnected. Canceled idle teardown timer.\n", s.Key)
	}

	ch := make(chan []byte, 300)
	s.Subscribers[ch] = struct{}{}
	return ch
}

func (s *VODSession) removeSubscriber(ch chan []byte) {
	s.SubMu.Lock()
	defer s.SubMu.Unlock()

	delete(s.Subscribers, ch)
	close(ch)

	// If no viewers remain, start a 15-second grace countdown before teardown
	if len(s.Subscribers) == 0 && s.IdleTimer == nil {
		log.Printf("[VOD %s] 0 active viewers. Starting 15s idle timer before releasing tuner...\n", s.Key)
		s.IdleTimer = time.AfterFunc(15*time.Second, func() {
			s.SubMu.Lock()
			count := len(s.Subscribers)
			s.SubMu.Unlock()

			if count == 0 {
				log.Printf("[VOD %s] Idle timer expired. Tearing down session.\n", s.Key)
				s.Cancel()
			}
		})
	}
}

func (s *VODSession) broadcast(data []byte) {
	s.SubMu.Lock()
	defer s.SubMu.Unlock()

	for ch := range s.Subscribers {
		select {
		case ch <- data:
		default:
			// Drop chunk for this subscriber if channel buffer is full to prevent blocking the capture pipe
		}
	}
}

func vodStreamHandler(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	rawLink := r.URL.Query().Get("link")
	durationStr := r.URL.Query().Get("duration")

	if providerID == "" || rawLink == "" {
		http.Error(w, "Missing provider or link", http.StatusBadRequest)
		return
	}

	sessionKey := fmt.Sprintf("%s:%s", providerID, rawLink)

	vodSessionsMu.Lock()
	session, exists := vodSessions[sessionKey]

	if !exists {
		// No session exists; allocate a tuner
		tuner := lockTuner()
		if tuner == nil {
			vodSessionsMu.Unlock()
			http.Error(w, "All tuners are currently in use", http.StatusServiceUnavailable)
			return
		}

		// Calculate total timeout duration
		sessionDuration := 4 * time.Hour // Default fallback
		if durationStr != "" {
			var durationMinutes int
			if _, err := fmt.Sscanf(durationStr, "%d", &durationMinutes); err == nil && durationMinutes > 0 {
				totalMinutes := int(float64(durationMinutes)*1.30) + 5
				sessionDuration = time.Duration(totalMinutes) * time.Minute
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), sessionDuration)

		session = &VODSession{
			Key:         sessionKey,
			Tuner:       tuner,
			Cancel:      cancel,
			Subscribers: make(map[chan []byte]struct{}),
			Done:        make(chan struct{}),
		}
		vodSessions[sessionKey] = session
		vodSessionsMu.Unlock()

		log.Printf("[VOD %s] Created new session on Tuner %s\n", sessionKey, tuner.DeviceIP)

		// Start tuning and capture pipeline in background
		go func() {
			defer func() {
				vodSessionsMu.Lock()
				delete(vodSessions, sessionKey)
				vodSessionsMu.Unlock()

				releaseTuner(tuner.DeviceIP)
				close(session.Done)
				log.Printf("[VOD %s] Capture pipeline terminated and tuner released.\n", sessionKey)
			}()

			// 1. RUN TUNING ASYNCHRONOUSLY 
			// This allows FFmpeg to start immediately below.
			go executeVODTuning(ctx, tuner.DeviceIP, providerID, rawLink)

			// 2. HARDCODE BLACKOUT TO MATCH ADB WARMUP (500ms wake + 3500ms sleep = 4000ms)
			blackoutMs := 4000 

			// Start capture pipeline
			if tuner.Type == "local" {
				ffmpeg := getFFmpegPath()
				captureFormat := tuner.CaptureFormat
				if captureFormat == "" {
					captureFormat = "mjpeg"
				}
				captureSize := tuner.CaptureSize
				if captureSize == "" {
					captureSize = "1920x1080"
				}
				captureFPS := tuner.CaptureFPS
				if captureFPS == "" {
					captureFPS = "60"
				}

				formatFlag := "-vcodec"
				if captureFormat == "nv12" || captureFormat == "yuyv422" || captureFormat == "yuy2" {
					formatFlag = "-pixel_format"
				}

				vfArg := "setpts=PTS-STARTPTS,format=nv12"
				if tuner.DeinterlaceMode == "tff" {
					vfArg = "setpts=PTS-STARTPTS,bwdif=mode=1:parity=0,format=nv12"
				} else if tuner.DeinterlaceMode == "bff" {
					vfArg = "setpts=PTS-STARTPTS,bwdif=mode=1:parity=1,format=nv12"
				}

				// ADD THE BLACKOUT DRAWBOX FILTER
				if blackoutMs > 0 {
					splashSecs := float64(blackoutMs) / 1000.0
					vfArg += fmt.Sprintf(",drawbox=x=0:y=0:w=iw:h=ih:color=black:t=fill:enable='lte(t,%.2f)'", splashSecs)
				}

				args := []string{"-hide_banner", "-loglevel", "error"}
				dshowInput := fmt.Sprintf("video=%s", tuner.VideoDeviceID)
				if tuner.AudioDeviceID != "" {
					dshowInput += fmt.Sprintf(":audio=%s", tuner.AudioDeviceID)
				}

				encoderArgs := getEncoderArgs()
				isQSV := false
				for _, a := range encoderArgs {
					if a == "h264_qsv" {
						isQSV = true
						break
					}
				}
				if isQSV {
					args = append(args, "-init_hw_device", "qsv=hw", "-filter_hw_device", "hw")
				}

				args = append(args,
					"-rtbufsize", "256M",
					"-thread_queue_size", "1024",
					"-f", "dshow",
					"-video_size", captureSize,
					"-framerate", captureFPS,
					formatFlag, captureFormat,
					"-i", dshowInput,
					"-vf", vfArg,
				)
				args = append(args, encoderArgs...)
				args = append(args,
					"-color_primaries", "bt709",
					"-color_trc", "bt709",
					"-colorspace", "bt709",
					"-color_range", "tv",
				)

				afArg := "asetpts=PTS-STARTPTS"
				if tuner.AudioDelayMs > 0 {
					afArg += fmt.Sprintf(",adelay=%d|%d", tuner.AudioDelayMs, tuner.AudioDelayMs)
				}
				
				// ADD THE BLACKOUT VOLUME FILTER
				if blackoutMs > 0 {
					splashSecs := float64(blackoutMs) / 1000.0
					afArg += fmt.Sprintf(",volume=enable='lte(t,%.2f)':volume=0", splashSecs)
				}
				afArg += ",aresample=async=1"
				args = append(args, "-af", afArg)
				args = append(args,
					"-c:a", "aac", "-b:a", "192k", "-ar", "48000",
					"-f", "mpegts", "-muxrate", "10M",
					"pipe:1",
				)

				cmd := exec.CommandContext(ctx, ffmpeg, args...)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

				stdout, err := cmd.StdoutPipe()
				if err != nil {
					log.Println("FFmpeg stdout pipe error:", err)
					return
				}
				if err := cmd.Start(); err != nil {
					log.Println("FFmpeg start error:", err)
					return
				}

				buf := make([]byte, 32*1024)
				for {
					n, err := stdout.Read(buf)
					if n > 0 {
						chunk := make([]byte, n)
						copy(chunk, buf[:n])
						session.broadcast(chunk)
					}
					if err != nil {
						break
					}
				}
				cmd.Process.Kill()
				cmd.Wait()
			} else {
				// Network Encoder route - Delay connection until ADB is done
				if blackoutMs > 0 {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Duration(blackoutMs) * time.Millisecond):
					}
				}
				
				req, err := http.NewRequestWithContext(ctx, "GET", tuner.EncoderURL, nil)
				if err != nil {
					return
				}
				resp, err := streamClient.Do(req)
				if err != nil {
					return
				}
				defer resp.Body.Close()

				buf := make([]byte, 64*1024)
				for {
					n, err := resp.Body.Read(buf)
					if n > 0 {
						chunk := make([]byte, n)
						copy(chunk, buf[:n])
						session.broadcast(chunk)
					}
					if err != nil {
						break
					}
				}
			}
		}()
	} else {
		vodSessionsMu.Unlock()
		log.Printf("[VOD %s] Attaching incoming request to existing session on Tuner %s\n", sessionKey, session.Tuner.DeviceIP)
	}

	// Subscribe this HTTP request to the session stream
	subChan := session.addSubscriber()
	defer session.removeSubscriber(subChan)

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Tuner-Device-IP", session.Tuner.DeviceIP)
	w.Header().Set("X-Tuner-Name", session.Tuner.Name)
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	clientCtx := r.Context()
	for {
		select {
		case <-clientCtx.Done():
			// Client probe or playback socket closed
			return
		case <-session.Done:
			// Session was killed (timeout or idle expiry)
			return
		case chunk, ok := <-subChan:
			if !ok {
				return
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func generateM3U(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	fmt.Fprintf(w, "#EXTM3U x-tvh-max-streams=%d\n", len(Config.Tuners))
	localIP := getLocalIP()
	
	// Grab the "group" parameter from the URL query (convert to lowercase for safe comparison)
	targetGroup := strings.ToLower(r.URL.Query().Get("group"))

	for _, ch := range Config.Channels {
		// If a specific group was requested, skip channels that do not match
		if targetGroup != "" && strings.ToLower(ch.GroupTitle) != targetGroup {
			continue
		}

		stationData := ""
		if ch.TvcGuideStationID != "" {
			stationData += fmt.Sprintf(` tvc-guide-stationid="%s"`, ch.TvcGuideStationID)
		}
		if ch.TvgShift != "" {
			stationData += fmt.Sprintf(` tvg-shift="%s"`, ch.TvgShift)
		}
		if ch.TvgLogo != "" {
			stationData += fmt.Sprintf(` tvg-logo="%s"`, ch.TvgLogo)
		}
		if ch.GroupTitle != "" {
			stationData += fmt.Sprintf(` group-title="%s"`, ch.GroupTitle)
		}
		
		fmt.Fprintf(w, "#EXTINF:-1 channel-id=\"%s\"%s,%s\n", ch.ID, stationData, ch.Name)
		fmt.Fprintf(w, "http://%s:%d/stream/%s\n", localIP, Config.Port, ch.ID)
	}
}

func remotePage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(embeddedFiles, "templates/remote.html")
	if err != nil {
		http.Error(w, "Could not load remote template", http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

func remoteKeypress(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	deviceIP := parts[3]
	key := parts[4]

	keyMap := map[string]string{
		"Home": "3", "Back": "4", "Select": "23", "Enter": "23",
		"Up": "19", "Down": "20", "Left": "21", "Right": "22",
		"Play": "85", "Pause": "85", "Rev": "89", "Fwd": "90",
		"Info": "82",
	}

	adbKey, exists := keyMap[key]
	if !exists {
		http.Error(w, "Unknown key", http.StatusBadRequest)
		return
	}

	go adbCommand(deviceIP, "shell", "input", "keyevent", adbKey)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
}

func previewPage(w http.ResponseWriter, r *http.Request) {
	channelID := strings.TrimPrefix(r.URL.Path, "/preview/")

	var channel *Channel
	for _, c := range Config.Channels {
		if c.ID == channelID {
			channel = &c
			break
		}
	}

	if channel == nil {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}

	tmpl, err := template.ParseFS(embeddedFiles, "templates/preview.html")
	if err != nil {
		http.Error(w, "Could not load preview template", http.StatusInternalServerError)
		return
	}

	tmpl.Execute(w, channel)
}

func checkTuners(w http.ResponseWriter, r *http.Request) {
	type StatusResult struct {
		DeviceIP      string `json:"device_ip"`
		ADBOnline     bool   `json:"adb_online"`
		EncoderOnline bool   `json:"encoder_online"`
	}
	var results []StatusResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	tunerLock.Lock()
	tuners := make([]Tuner, len(Config.Tuners))
	copy(tuners, Config.Tuners)
	tunerLock.Unlock()

	for _, t := range tuners {
		wg.Add(1)

		go func(tuner Tuner) {
			defer wg.Done()

			res := StatusResult{DeviceIP: tuner.DeviceIP}
			res.ADBOnline = checkADB(tuner)

			if tuner.Type == "local" {
				if _, err := os.Stat(getFFmpegPath()); err == nil {
					res.EncoderOnline = true
				} else {
					res.EncoderOnline = false
				}
			} else if tuner.EncoderURL != "" {
				client := http.Client{Timeout: 2 * time.Second}
				resp, err := client.Get(tuner.EncoderURL)
				if err == nil {
					resp.Body.Close()
					res.EncoderOnline = resp.StatusCode < 500
				} else {
					res.EncoderOnline = false
				}
			}

			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}(t)
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// ==========================================
// 7. Main Initialization
// ==========================================
func apiRestartADB(w http.ResponseWriter, r *http.Request) {
	adb := getAdbPath()
	log.Println("Manually restarting ADB server via dashboard...")
	
	// Kill the server
	killCmd := exec.Command(adb, "kill-server")
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	killCmd.Run()
	
	// Start it back up
	ensureADBReady()
	
	// Reconnect all network tuners
	for _, t := range Config.Tuners {
		if t.AdbRoute != "usb" {
			exec.Command(adb, "connect", t.DeviceIP).Run()
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
}

func apiScreencap(w http.ResponseWriter, r *http.Request) {
	deviceIP := strings.TrimPrefix(r.URL.Path, "/api/screencap/")
	if deviceIP == "" {
		http.Error(w, "Invalid IP", http.StatusBadRequest)
		return
	}

	adb := getAdbPath()
	cmd := exec.Command(adb, "-s", deviceIP, "exec-out", "screencap", "-p")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.Output()
	if err != nil {
		http.Error(w, "Screencap failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(out)
}

func apiInspectApp(w http.ResponseWriter, r *http.Request) {
	deviceIP := strings.TrimPrefix(r.URL.Path, "/api/inspect_app/")
	if deviceIP == "" {
		http.Error(w, "Invalid IP", http.StatusBadRequest)
		return
	}

	// 1. Check the modern Android 11+ displays output
	out, err := adbCommand(deviceIP, "shell", "dumpsys", "window", "displays")
	if err != nil {
		http.Error(w, "Inspect failed", http.StatusInternalServerError)
		return
	}

	var pkg, comp string
	re := regexp.MustCompile(`([a-zA-Z0-9_.]+)/([a-zA-Z0-9_.]+)`)
	
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "mCurrentFocus") || strings.Contains(line, "mFocusedApp") {
			matches := re.FindStringSubmatch(line)
			if len(matches) == 3 && matches[1] != "null" {
				pkg = matches[1]
				comp = matches[2]
				break
			}
		}
	}

	// 2. Fallback to the Activity Manager if the Window Manager didn't return anything (Common on Fire OS)
	if pkg == "" {
		outAct, errAct := adbCommand(deviceIP, "shell", "dumpsys", "activity", "activities")
		if errAct == nil {
			linesAct := strings.Split(outAct, "\n")
			for _, line := range linesAct {
				if strings.Contains(line, "mResumedActivity") || strings.Contains(line, "topResumedActivity") {
					matches := re.FindStringSubmatch(line)
					if len(matches) == 3 && matches[1] != "null" {
						pkg = matches[1]
						comp = matches[2]
						break
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"package":   pkg,
		"component": comp,
	})
}

type ToolRequest struct {
	DeviceIP string `json:"device_ip"`
	Action   string `json:"action"`
	Param    string `json:"param"`
}

func apiAdbTools(w http.ResponseWriter, r *http.Request) {
if r.Method != "POST" {
http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
return
}

var req ToolRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
http.Error(w, "Invalid request", http.StatusBadRequest)
return
}

switch req.Action {
case "reboot":
go adbCommand(req.DeviceIP, "reboot")
case "stay_awake":
adbCommand(req.DeviceIP, "shell", "settings", "put", "global", "stay_on_while_plugged_in", "7")
adbCommand(req.DeviceIP, "shell", "settings", "put", "system", "screen_off_timeout", "2147483647")
case "trim_caches":
adbCommand(req.DeviceIP, "shell", "pm", "trim-caches", "9999999999")
case "input_text":
if req.Param != "" {
escapedText := strings.ReplaceAll(req.Param, " ", "%s")
adbCommand(req.DeviceIP, "shell", "input", "text", escapedText)
}
case "lock_1080p":
adbCommand(req.DeviceIP, "shell", "wm", "size", "1920x1080")
adbCommand(req.DeviceIP, "shell", "wm", "density", "320")
case "reset_display":
adbCommand(req.DeviceIP, "shell", "wm", "size", "reset")
adbCommand(req.DeviceIP, "shell", "wm", "density", "reset")
}

w.Header().Set("Content-Type", "application/json")
w.Write([]byte(`{"status": "success"}`))
}

func getAdbKeyDir() string {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "AndroidADBBridge")
}

func apiExportAdbKeys(w http.ResponseWriter, r *http.Request) {
	keyDir := getAdbKeyDir()
	privKey := filepath.Join(keyDir, "adbkey")
	pubKey := filepath.Join(keyDir, "adbkey.pub")

	if _, err := os.Stat(privKey); os.IsNotExist(err) {
		http.Error(w, "No ADB keys found to export", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="adb_key_vault.zip"`)

	zipWriter := zip.NewWriter(w)
	defer zipWriter.Close()

	files := []string{privKey, pubKey}
	for _, filePath := range files {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			continue
		}

		fileData, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		entryWriter, err := zipWriter.Create(filepath.Base(filePath))
		if err != nil {
			continue
		}
		entryWriter.Write(fileData)
	}
}

func apiImportAdbKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "Error parsing form", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("keyVaultFile")
	if err != nil {
		http.Error(w, "Error retrieving file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	tempZip, err := os.CreateTemp("", "adb_keys_*.zip")
	if err != nil {
		http.Error(w, "Failed to create temp file", http.StatusInternalServerError)
		return
	}
	defer os.Remove(tempZip.Name())
	defer tempZip.Close()

	if _, err := io.Copy(tempZip, file); err != nil {
		http.Error(w, "Failed to read upload", http.StatusInternalServerError)
		return
	}

	zipReader, err := zip.OpenReader(tempZip.Name())
	if err != nil {
		http.Error(w, "Uploaded file is not a valid zip archive", http.StatusBadRequest)
		return
	}
	defer zipReader.Close()

	keyDir := getAdbKeyDir()
	os.MkdirAll(keyDir, os.ModePerm)

	foundKeys := 0
	for _, zf := range zipReader.File {
		baseName := filepath.Base(zf.Name)
		if baseName != "adbkey" && baseName != "adbkey.pub" {
			continue
		}

		rc, err := zf.Open()
		if err != nil {
			continue
		}

		destPath := filepath.Join(keyDir, baseName)
		outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err == nil {
			io.Copy(outFile, rc)
			outFile.Close()
			foundKeys++
		}
		rc.Close()
	}

	if foundKeys == 0 {
		http.Error(w, "Archive did not contain valid adbkey or adbkey.pub files", http.StatusBadRequest)
		return
	}

	// Restart the ADB daemon so it immediately drops stale memory keys and loads the imported pair
	adb := getAdbPath()
	exec.Command(adb, "kill-server").Run()
	ensureADBReady()

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
}

func main() {
	uiFlag := flag.Bool("ui", false, "Open the web dashboard in the default browser")
	portFlag := flag.Int("port", 0, "Override the port the server listens on (e.g., 8888)")
	flag.Parse()

	loadConfig()
	setupLogging()

	if *portFlag > 0 {
		Config.Port = *portFlag
		saveConfig()
	}

	if *uiFlag {
		localIP := getLocalIP()
		targetURL := fmt.Sprintf("http://%s:%d/status", localIP, Config.Port)

		cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Start()
		
		return 
	}

	ensureADBReady()

	http.HandleFunc("/", statusPage)
	http.HandleFunc("/status", statusPage)
	http.HandleFunc("/health", healthCheck)
	http.HandleFunc("/api/config", apiConfig)
	http.HandleFunc("/api/export_config", apiExportConfig)
	http.HandleFunc("/api/import_config", apiImportConfig)
	http.HandleFunc("/api/devices", apiDevices)
	http.HandleFunc("/api/download_log", apiDownloadLog)
	http.HandleFunc("/api/probe_device", apiProbeDevice)
	http.HandleFunc("/api/active_tuners", apiActiveTuners)
	http.HandleFunc("/stream/", streamHandler)
	http.HandleFunc("/vod", vodStreamHandler)
	http.HandleFunc("/channels.m3u", generateM3U)
	http.HandleFunc("/remote", remotePage)
	http.HandleFunc("/remote/keypress/", remoteKeypress)
	http.HandleFunc("/preview/", previewPage)
	http.HandleFunc("/api/check_tuners", checkTuners)
	http.HandleFunc("/api/release/", apiReleaseTuner)
	http.HandleFunc("/api/usb_devices", apiUsbDevices)
	http.HandleFunc("/api/restart_adb", apiRestartADB)
	http.HandleFunc("/api/check_update", apiCheckUpdate)
	http.HandleFunc("/api/apply_update", apiApplyUpdate)
    http.HandleFunc("/api/screencap/", apiScreencap)
	http.HandleFunc("/api/inspect_app/", apiInspectApp)
	http.HandleFunc("/api/tools", apiAdbTools)
	http.HandleFunc("/api/export_adb_keys", apiExportAdbKeys)
	http.HandleFunc("/api/import_adb_keys", apiImportAdbKeys)
	
	portString := fmt.Sprintf(":%d", Config.Port)
	log.Printf("ADB Bridge server preparing to listen on %s\n", portString)

	var listener net.Listener
	var listenErr error

	// Retry binding for up to 60 seconds to survive post-update boot delays
	for attempt := 1; attempt <= 20; attempt++ {
		listener, listenErr = net.Listen("tcp", portString)
		if listenErr == nil {
			break
		}
		log.Printf("Port %s not ready yet (attempt %d/20): %v. Retrying in 3s...\n", portString, attempt, listenErr)
		time.Sleep(3 * time.Second)
	}

	if listenErr != nil {
		log.Fatalf("Server startup failed: could not bind port %s after 20 attempts: %v\n", portString, listenErr)
	}

	log.Printf("ADB Bridge server successfully listening on %s\n", portString)
	if err := http.Serve(listener, nil); err != nil {
		log.Fatalf("Server terminated: %v\n", err)
	}
}