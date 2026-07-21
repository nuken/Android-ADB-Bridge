package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
	Name          string `json:"name"`
	DeviceIP      string `json:"device_ip"`
	Type          string `json:"type"` // "network" or "local"
	EncoderURL    string `json:"encoder_url,omitempty"`
	VideoDeviceID string `json:"video_device_id,omitempty"`
	AudioDeviceID string `json:"audio_device_id,omitempty"`
	Priority      int    `json:"priority"`
	InUse         bool   `json:"-"`
}

type Provider struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Intent      string `json:"intent"`
	URLTemplate string `json:"url_template"`
}

type Channel struct {
	Name              string `json:"name"`
	ID                string `json:"id"`
	ProviderID        string `json:"provider_id"`
	DeepLinkContentID string `json:"deep_link_content_id"`
	TvcGuideStationID string `json:"tvc_guide_stationid"`
}

type AppConfig struct {
	Port      int        `json:"port"`
	Tuners    []Tuner    `json:"tuners"`
	Providers []Provider `json:"providers"`
	Channels  []Channel  `json:"channels"`
}

// Structs for FFmpeg Device Discovery
type DShowDevice struct {
	Name string `json:"name"`
	ID   string `json:"id"` // The "Alternative Name" hardware path
}

type DeviceList struct {
	Video []DShowDevice `json:"video"`
	Audio []DShowDevice `json:"audio"`
}

var Config AppConfig
var AppVersion = "5.0.3-GO"
var tunerLock sync.Mutex

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
					Intent:      "com.google.android.youtube.tvunplugged/com.google.android.apps.youtube.tvunplugged.activity.MainActivity",
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

	// Migration: Ensure older configs default to network type
	for i := range Config.Tuners {
		if Config.Tuners[i].Type == "" {
			Config.Tuners[i].Type = "network"
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

func adbCommand(deviceIP string, args ...string) error {
	adb := getAdbPath()

	connectCmd := exec.Command(adb, "connect", deviceIP)
	connectCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	connectCmd.Run()

	fullArgs := append([]string{"-s", deviceIP}, args...)
	cmd := exec.Command(adb, fullArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	return cmd.Run()
}

func lockTuner() *Tuner {
	tunerLock.Lock()
	defer tunerLock.Unlock()
	for i := range Config.Tuners {
		if !Config.Tuners[i].InUse {
			Config.Tuners[i].InUse = true
			return &Config.Tuners[i]
		}
	}
	return nil
}

func releaseTuner(deviceIP string) {
	tunerLock.Lock()
	defer tunerLock.Unlock()
	for i := range Config.Tuners {
		if Config.Tuners[i].DeviceIP == deviceIP {
			Config.Tuners[i].InUse = false
			log.Printf("Released tuner %s. Sending Home command.\n", deviceIP)
			go adbCommand(deviceIP, "shell", "input", "keyevent", "3")
			break
		}
	}
}

func executeTuning(deviceIP string, ch Channel) {
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

	targetURL := strings.ReplaceAll(provider.URLTemplate, "{id}", ch.DeepLinkContentID)
	log.Printf("Tuning %s to %s via %s\n", deviceIP, ch.Name, provider.Name)

	adbCommand(deviceIP, "shell", "input", "keyevent", "224")
	time.Sleep(1 * time.Second)

	adbCommand(deviceIP, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", targetURL, "-n", provider.Intent)
}

// ==========================================
// 5. FFmpeg Hardware Discovery
// ==========================================
func apiDevices(w http.ResponseWriter, r *http.Request) {
	ffmpeg := getFFmpegPath()
	cmd := exec.Command(ffmpeg, "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	// FFmpeg writes device lists to stderr, not stdout
	out, _ := cmd.CombinedOutput()
	lines := strings.Split(string(out), "\n")

	devices := DeviceList{Video: []DShowDevice{}, Audio: []DShowDevice{}}
	var currentType string
	var lastDevice *DShowDevice

	for _, line := range lines {
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
			if len(parts) >= 3 {
				val := parts[1]
				if strings.Contains(line, "Alternative name") {
					// Apply the hardware path ID to the last found device
					if lastDevice != nil {
						lastDevice.ID = val
					}
				} else {
					// It's a new device name. We set ID to Name as a fallback.
					newDev := DShowDevice{Name: val, ID: val}
					if currentType == "video" {
						devices.Video = append(devices.Video, newDev)
						lastDevice = &devices.Video[len(devices.Video)-1]
					} else if currentType == "audio" {
						devices.Audio = append(devices.Audio, newDev)
						lastDevice = &devices.Audio[len(devices.Audio)-1]
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

// streamHandler branches based on Tuner Type (Local USB vs Network Encoder)
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

	executeTuning(tuner.DeviceIP, *channel)

	// Common HTTP headers for MPEG-TS
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")

	// ==========================================
	// BRANCH A: Local USB Capture (FFmpeg)
	// ==========================================
	if tuner.Type == "local" {
		// Give the Android stick a moment to transition screens before capturing
		time.Sleep(2 * time.Second)

		ffmpeg := getFFmpegPath()
		inputStr := fmt.Sprintf("video=%s:audio=%s", tuner.VideoDeviceID, tuner.AudioDeviceID)

		// Command tells FFmpeg to ingest the specific DirectShow hardware ID, encode lightly via ultrafast preset, and output to standard pipe
		args := []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "dshow",
			"-i", inputStr,
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "192k", "-ar", "48000",
			"-f", "mpegts",
			"pipe:1",
		}

		cmd := exec.Command(ffmpeg, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Println("FFmpeg stdout error:", err)
			http.Error(w, "Capture card initialization failed", http.StatusInternalServerError)
			return
		}

		if err := cmd.Start(); err != nil {
			log.Println("FFmpeg start error:", err)
			http.Error(w, "Failed to start FFmpeg", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)

		// Pipe the FFmpeg stdout directly to the HTTP response
		buf := make([]byte, 128*1024)
		io.CopyBuffer(w, stdout, buf)

		// Ensure FFmpeg dies immediately when Channels DVR disconnects
		cmd.Process.Kill()
		cmd.Wait()
		return
	}

	// ==========================================
	// BRANCH B: Network Encoder (LinkPi)
	// ==========================================
	time.Sleep(2 * time.Second)

	req, err := http.NewRequestWithContext(r.Context(), "GET", tuner.EncoderURL, nil)
	if err != nil {
		http.Error(w, "Invalid encoder URL", http.StatusInternalServerError)
		return
	}

	resp, err := streamClient.Do(req)
	if err != nil {
		log.Println("Encoder connection error:", err)
		http.Error(w, "Failed to connect to encoder", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(http.StatusOK)

	buf := make([]byte, 128*1024)
	_, err = io.CopyBuffer(w, resp.Body, buf)
	if err != nil {
		log.Printf("Stream closed or client disconnected: %v\n", err)
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
		"Home": "3", "Back": "4", "Select": "66", "Enter": "66",
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

type TunerStatus struct {
	DeviceIP      string `json:"device_ip"`
	AdbOnline     bool   `json:"adb_online"`
	EncoderOnline bool   `json:"encoder_online"`
}

func checkTuners(w http.ResponseWriter, r *http.Request) {
	var statuses []TunerStatus
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, t := range Config.Tuners {
		wg.Add(1)

		go func(tuner Tuner) {
			defer wg.Done()

			encoderOk := false

			// Differentiate health checks based on the hardware type
			if tuner.Type == "local" {
				// For local USB dongles, verify FFmpeg is present
				_, err := os.Stat(getFFmpegPath())
				encoderOk = (err == nil)
			} else {
				// For network encoders, execute HTTP ping
				req, err := http.NewRequest("GET", tuner.EncoderURL, nil)
				if err == nil {
					client := http.Client{Timeout: 2 * time.Second}
					resp, err := client.Do(req)
					if err == nil {
						encoderOk = (resp.StatusCode == 200)
						resp.Body.Close()
					}
				}
			}

			adb := getAdbPath()
			cmd := exec.Command(adb, "connect", tuner.DeviceIP)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			out, _ := cmd.Output()
			outStr := strings.ToLower(string(out))
			adbOk := strings.Contains(outStr, "connected")

			mu.Lock()
			statuses = append(statuses, TunerStatus{
				DeviceIP:      tuner.DeviceIP,
				AdbOnline:     adbOk,
				EncoderOnline: encoderOk,
			})
			mu.Unlock()
		}(t)
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}

// ==========================================
// 7. Main Initialization
// ==========================================
func main() {
	uiFlag := flag.Bool("ui", false, "Open the web dashboard in the default browser")
	flag.Parse()

	loadConfig()

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
	http.HandleFunc("/api/devices", apiDevices)
	http.HandleFunc("/api/active_tuners", apiActiveTuners)
	http.HandleFunc("/stream/", streamHandler)
	http.HandleFunc("/channels.m3u", generateM3U)
	http.HandleFunc("/remote", remotePage)
	http.HandleFunc("/remote/keypress/", remoteKeypress)
	http.HandleFunc("/preview/", previewPage)
	http.HandleFunc("/api/check_tuners", checkTuners)

	portString := fmt.Sprintf(":%d", Config.Port)
	log.Printf("ADB Bridge server listening on %s\n", portString)
	if err := http.ListenAndServe(portString, nil); err != nil {
		log.Fatalf("Server startup failed: %v\n", err)
	}
}