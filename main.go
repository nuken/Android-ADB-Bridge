package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
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
	Name       string `json:"name"`
	DeviceIP   string `json:"device_ip"`
	EncoderURL string `json:"encoder_url"`
	Priority   int    `json:"priority"`
	InUse      bool   `json:"-"`
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

var Config AppConfig
var AppVersion = "5.0.0-GO"
var tunerLock sync.Mutex // Prevents race conditions when locking tuners

// ==========================================
// App Initialization
// ==========================================
func init() {
	// Define a shared, system-wide path for ADB keys (C:\ProgramData\AndroidADBBridge\.android)
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}

	sharedAdbPath := filepath.Join(programData, "AndroidADBBridge")

	// Ensure the directory exists
	os.MkdirAll(sharedAdbPath, os.ModePerm)

	// Force ADB to use this shared directory for its keys instead of the user profile
	os.Setenv("ANDROID_USER_HOME", sharedAdbPath)

	// Fallback for older ADB versions
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

// getAvailablePort finds the next open network port starting from a given number
func getAvailablePort(startPort int) int {
	port := startPort
	for {
		// Attempt to open the port
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			// Port is available! Close our test connection and return the number
			ln.Close()
			return port
		}
		// Port is in use, increment and try the next one
		port++
	}
}

func loadConfig() {
	configPath := getConfigPath()
	configDir := filepath.Dir(configPath)
	os.MkdirAll(configDir, os.ModePerm)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Automatically find an open port starting at 8888
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
}

func saveConfig() {
	fileData, _ := json.MarshalIndent(Config, "", "  ")
	os.WriteFile(getConfigPath(), fileData, 0644)
}

// ==========================================
// 3. ADB & Tuning Logic
// ==========================================

// getAdbPath finds the bundled adb.exe sitting next to our app
func getAdbPath() string {
	exePath, err := os.Executable()
	if err != nil {
		return "adb" // Fallback to system path if error
	}
	return filepath.Join(filepath.Dir(exePath), "adb.exe")
}

// adbCommand wraps os/exec to run ADB commands without flashing a cmd window
func adbCommand(deviceIP string, args ...string) error {
	adb := getAdbPath() // Get the exact path

	// Reconnect first to prevent offline errors
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
			fmt.Printf("Released tuner %s. Sending Home command.\n", deviceIP)

			// Send the Home key in the background so it doesn't block the exit
			go adbCommand(deviceIP, "shell", "input", "keyevent", "3")
			break
		}
	}
}

func executeTuning(deviceIP string, ch Channel) {
	// 1. Locate the correct provider
	var provider *Provider
	for _, p := range Config.Providers {
		if p.ID == ch.ProviderID {
			provider = &p
			break
		}
	}

	if provider == nil {
		fmt.Printf("Error: Provider '%s' not found for channel '%s'\n", ch.ProviderID, ch.Name)
		return
	}

	// 2. Build the exact URL dynamically
	targetURL := strings.ReplaceAll(provider.URLTemplate, "{id}", ch.DeepLinkContentID)
	fmt.Printf("Tuning %s to %s via %s\n", deviceIP, ch.Name, provider.Name)

	// 3. Wake the device
	adbCommand(deviceIP, "shell", "input", "keyevent", "224")
	time.Sleep(1 * time.Second)

	// 4. Fire the Deep Link Intent
	adbCommand(deviceIP, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", targetURL, "-n", provider.Intent)
}

// ==========================================
// 4. Web Endpoints
// ==========================================

// getLocalIP safely finds the home network IP, ignoring VPNs and Loopbacks
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, address := range addrs {
			// Check if the address is an IPv4 and NOT a loopback (127.0.0.1)
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				ip := ipnet.IP.String()

				// Standard home routers almost always use 192.168.x.x
				// We prioritize this to bypass virtual adapters from VPNs or VMware
				if strings.HasPrefix(ip, "192.168.") {
					return ip
				}
				// Secondary fallbacks for less common home networks (10.x.x.x or 172.16-31.x.x)
				if strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "172.") {
					return ip
				}
			}
		}
	}
	return "127.0.0.1" // Absolute fallback
}

func statusPage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(embeddedFiles, "templates/status.html")
	if err != nil {
		fmt.Println("CRITICAL TEMPLATE ERROR:", err)
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

// streamHandler proxies the video from the LinkPi to the client
func streamHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the channel ID from the URL path
	channelID := strings.TrimPrefix(r.URL.Path, "/stream/")

	// Find the requested channel
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

	// Attempt to lock an available tuner
	tuner := lockTuner()
	if tuner == nil {
		http.Error(w, "All tuners are currently in use", http.StatusServiceUnavailable)
		return
	}

	// Ensure the tuner is released to the Home screen when the connection drops
	defer releaseTuner(tuner.DeviceIP)

	// Tune the Android device
	executeTuning(tuner.DeviceIP, *channel)

	// Give the LinkPi a moment to start encoding the new stream
	time.Sleep(2 * time.Second)

	// Connect to the raw TS stream
	resp, err := http.Get(tuner.EncoderURL)
	if err != nil {
		fmt.Println("Encoder connection error:", err)
		http.Error(w, "Failed to connect to encoder", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Set headers for raw TS streaming
	w.Header().Set("Content-Type", "video/mp2t")

	// io.Copy seamlessly pipes the stream from the encoder directly to the client
	io.Copy(w, resp.Body)
}

// generateM3U builds the playlist format required by IPTV clients
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

		// Lock the stream URL to the actual network IP instead of localhost
		fmt.Fprintf(w, "http://%s:%d/stream/%s\n", localIP, Config.Port, ch.ID)
	}
}

// remotePage serves the HTML for the remote control UI
func remotePage(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFS(embeddedFiles, "templates/remote.html")
	if err != nil {
		http.Error(w, "Could not load remote template", http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

// remoteKeypress translates friendly key names into ADB keycodes
func remoteKeypress(w http.ResponseWriter, r *http.Request) {
	// Expected URL pattern: /remote/keypress/{device_ip}/{key}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	deviceIP := parts[3]
	key := parts[4]

	// Map the friendly string names to Android KeyCodes
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

	// Fire the key event in a background thread so the UI doesn't hang
	go adbCommand(deviceIP, "shell", "input", "keyevent", adbKey)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status": "success"}`))
}

// previewPage serves the video player UI
func previewPage(w http.ResponseWriter, r *http.Request) {
	// Extract the channel ID from the URL path
	channelID := strings.TrimPrefix(r.URL.Path, "/preview/")

	// Find the requested channel
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

	// Pass the specific channel struct to the template
	tmpl.Execute(w, channel)
}

// TunerStatus holds the real-time connection state
type TunerStatus struct {
	DeviceIP      string `json:"device_ip"`
	AdbOnline     bool   `json:"adb_online"`
	EncoderOnline bool   `json:"encoder_online"`
}

// checkTuners concurrently tests the ADB and Encoder connections
func checkTuners(w http.ResponseWriter, r *http.Request) {
	var statuses []TunerStatus
	var wg sync.WaitGroup
	var mu sync.Mutex // Protects the slice during concurrent appends

	for _, t := range Config.Tuners {
		wg.Add(1)

		// Spin up a background goroutine for each tuner so they check simultaneously
		go func(tuner Tuner) {
			defer wg.Done()

			// 1. Check Encoder (Connect, get 200 OK, and immediately close to prevent stream download)
			encoderOk := false
			req, err := http.NewRequest("GET", tuner.EncoderURL, nil)
			if err == nil {
				client := http.Client{Timeout: 2 * time.Second}
				resp, err := client.Do(req)
				if err == nil {
					encoderOk = (resp.StatusCode == 200)
					resp.Body.Close()
				}
			}

			// 2. Check ADB (Attempt to connect and read output)
			adb := getAdbPath()
			cmd := exec.Command(adb, "connect", tuner.DeviceIP)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			out, _ := cmd.Output()
			outStr := strings.ToLower(string(out))
			// ADB returns "connected to <ip>" or "already connected" if successful
			adbOk := strings.Contains(outStr, "connected")

			// Lock the slice, append the result, and unlock
			mu.Lock()
			statuses = append(statuses, TunerStatus{
				DeviceIP:      tuner.DeviceIP,
				AdbOnline:     adbOk,
				EncoderOnline: encoderOk,
			})
			mu.Unlock()
		}(t)
	}

	// Wait for all concurrent checks to finish
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}

// ==========================================
// 5. Main Initialization
// ==========================================
func main() {
	// 1. Define the -ui command line flag
	uiFlag := flag.Bool("ui", false, "Open the web dashboard in the default browser")
	flag.Parse()

	// Load the config (which contains the active port)
	loadConfig()

	// 2. If launched via the Desktop Shortcut (which passes the -ui flag)
	if *uiFlag {
		localIP := getLocalIP()
		targetURL := fmt.Sprintf("http://%s:%d/status", localIP, Config.Port)

		// Use the native Windows API to open the default web browser invisibly
		cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Start()

		// Exit this instance immediately so it doesn't try to start a second server
		return
	}

	// 3. Normal background server startup
	http.HandleFunc("/", statusPage)
	http.HandleFunc("/status", statusPage)
	http.HandleFunc("/api/config", apiConfig)
	http.HandleFunc("/stream/", streamHandler)
	http.HandleFunc("/channels.m3u", generateM3U)
	http.HandleFunc("/remote", remotePage)
	http.HandleFunc("/remote/keypress/", remoteKeypress)
	http.HandleFunc("/preview/", previewPage)
	http.HandleFunc("/api/check_tuners", checkTuners)

	portString := fmt.Sprintf(":%d", Config.Port)
	http.ListenAndServe(portString, nil)
}
