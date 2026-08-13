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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
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
}

type Channel struct {
	Name              string `json:"name"`
	ID                string `json:"id"`
	ProviderID        string `json:"provider_id"`
	DeepLinkContentID string `json:"deep_link_content_id,omitempty"`
	TvcGuideStationID string `json:"tvc_guide_stationid,omitempty"`
	TuningMacro       string `json:"tuning_macro,omitempty"`
}

type AppConfig struct {
	Port      int        `json:"port"`
	Tuners    []Tuner    `json:"tuners"`
	Providers []Provider `json:"providers"`
	Channels  []Channel  `json:"channels"`
}

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

var Config AppConfig
var AppVersion = "5.1.1-WIN"
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

	os.Setenv("ANDROID_USER_HOME", sharedAdbPath)
	os.Setenv("ANDROID_SDK_HOME", sharedAdbPath)
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

	for i := 1; i <= 10; i++ {
		cmd := exec.Command(adb, "start-server")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			log.Println("ADB server initialized successfully.")
			return
		}
		log.Printf("Waiting for ADB daemon to start (attempt %d/10)...\n", i)
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
				
				// Look up the specific Tuner's OS for cleanup
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

		// ALWAYS send the Home command afterwards to exit the app
		log.Printf("Released tuner %s. Sending default Home command.\n", deviceIP)
		adbCommand(deviceIP, "shell", "input", "keyevent", "3")

		// 3. NOW WE RELEASE THE TUNER (Only after all cleanup ADB commands are done)
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
			out, err := adbCommand(deviceIP, "shell", "am", "start", "-a", "android.intent.action.MAIN", "-c", "android.intent.category.LEANBACK_LAUNCHER", "-n", intentStr)
			
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

	deviceIP := strings.TrimPrefix(r.URL.Path, "/api/release/")
	if deviceIP == "" {
		http.Error(w, "Invalid device IP", http.StatusBadRequest)
		return
	}

	tunerLock.Lock()
	found := false
	for i := range Config.Tuners {
		if Config.Tuners[i].DeviceIP == deviceIP {
			Config.Tuners[i].InUse = false
			found = true
			log.Printf("Manually force-released tuner %s via web dashboard.\n", deviceIP)
			break
		}
	}
	tunerLock.Unlock()

	if !found {
		http.Error(w, "Tuner not found", http.StatusNotFound)
		return
	}

	go adbCommand(deviceIP, "shell", "input", "keyevent", "3")

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
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status": "success"}`))
	}
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

	// 1. Launch tuning asynchronously so FFmpeg starts instantly
	// This covers both macros and deep-link launches in the background
	go func() {
		executeTuning(r.Context(), tuner.DeviceIP, *channel)
	}()

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	if tuner.Type == "local" {
		// 2. Removed time.Sleep(2 * time.Second) here

		var provider *Provider
		for _, p := range Config.Providers {
			if p.ID == channel.ProviderID {
				provider = &p
				break
			}
		}
		
		splashDelayMs := 0
		if provider != nil {
			splashDelayMs = provider.SplashDelayMs
		}

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

		vfArg := "format=nv12"
		if tuner.DeinterlaceMode == "tff" {
			vfArg = "bwdif=mode=1:parity=0,format=nv12"
		} else if tuner.DeinterlaceMode == "bff" {
			vfArg = "bwdif=mode=1:parity=1,format=nv12"
		}

		// 3. Apply the pure black drawbox overlay based on SplashDelayMs
		if splashDelayMs > 0 {
			splashSecs := float64(splashDelayMs) / 1000.0
			vfArg += fmt.Sprintf(",drawbox=x=0:y=0:w=iw:h=ih:color=black:t=fill:enable='between(t,0,%.2f)'", splashSecs)
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

		afArg := ""
		if splashDelayMs > 0 {
			splashSecs := float64(splashDelayMs) / 1000.0
			afArg = fmt.Sprintf("volume=enable='between(t,0,%.2f)':volume=0", splashSecs)
		}

		if tuner.AudioDelayMs > 0 {
			if afArg != "" {
				afArg += ","
			}
			afArg += fmt.Sprintf("adelay=%d|%d", tuner.AudioDelayMs, tuner.AudioDelayMs)
		}

		if afArg != "" {
			args = append(args, "-af", afArg)
		}

		args = append(args,
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000",
			"-f", "mpegts",
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

		streamChan := make(chan []byte, 500)
		flusher, canFlush := w.(http.Flusher)

		go func() {
			defer close(streamChan)
			for {
				buf := make([]byte, 32*1024)
				n, err := stdout.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					streamChan <- chunk
				}
				if err != nil {
					break
				}
			}
		}()

		streamLoop:
		for {
			select {
			case <-r.Context().Done():
				// Instantly break if the client disconnects
				break streamLoop
			case chunk, ok := <-streamChan:
				if !ok {
					// FFmpeg channel closed
					break streamLoop
				}
				if _, err := w.Write(chunk); err != nil {
					break streamLoop
				}
				if canFlush {
					flusher.Flush()
				}
			}
		}
		cmd.Process.Kill()
		cmd.Wait()
		return
	}

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
	flusher, canFlush := w.(http.Flusher)

	for {
		// Instantly break if the client disconnects
		if r.Context().Err() != nil {
			break
		}
		
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				log.Printf("Stream write error: %v\n", wErr)
				break
			}
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

func generateM3U(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	fmt.Fprintf(w, "#EXTM3U x-tvh-max-streams=%d\n", len(Config.Tuners))

	localIP := getLocalIP()

	for _, ch := range Config.Channels {
		stationData := ""
		if ch.TvcGuideStationID != "" {
			stationData = fmt.Sprintf(` tvc-guide-stationid="%s"`, ch.TvcGuideStationID)
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
func main() {
	uiFlag := flag.Bool("ui", false, "Open the web dashboard in the default browser")
	portFlag := flag.Int("port", 0, "Override the port the server listens on (e.g., 8888)")
	flag.Parse()

	loadConfig()

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
	http.HandleFunc("/api/probe_device", apiProbeDevice)
	http.HandleFunc("/api/active_tuners", apiActiveTuners)
	http.HandleFunc("/stream/", streamHandler)
	http.HandleFunc("/channels.m3u", generateM3U)
	http.HandleFunc("/remote", remotePage)
	http.HandleFunc("/remote/keypress/", remoteKeypress)
	http.HandleFunc("/preview/", previewPage)
	http.HandleFunc("/api/check_tuners", checkTuners)
	http.HandleFunc("/api/release/", apiReleaseTuner)
	http.HandleFunc("/api/usb_devices", apiUsbDevices)

	portString := fmt.Sprintf(":%d", Config.Port)

	log.Printf("ADB Bridge server listening on %s\n", portString)
	if err := http.ListenAndServe(portString, nil); err != nil {
		log.Fatalf("Server startup failed: %v\n", err)
	}
}