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
	Type            string `json:"type"` // "network" or "local"
	EncoderURL      string `json:"encoder_url,omitempty"`
	VideoDeviceID   string `json:"video_device_id,omitempty"`
	AudioDeviceID   string `json:"audio_device_id,omitempty"`
	AudioDelayMs    int    `json:"audio_delay_ms,omitempty"`
	DeinterlaceMode string `json:"deinterlace_mode,omitempty"`
	VideoCodec      string `json:"video_codec,omitempty"`
	EncoderPreset   string `json:"encoder_preset,omitempty"`
	VideoBitrate    int    `json:"video_bitrate,omitempty"`
	AudioBitrate    int    `json:"audio_bitrate,omitempty"`
	Priority        int    `json:"priority"`
	InUse           bool   `json:"-"`
}

type Provider struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Intent      string `json:"intent,omitempty"`
	PackageName string `json:"package_name"`
	Component   string `json:"component"`
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

type DShowDevice struct {
	Name string `json:"name"`
	ID   string `json:"id"` 
}

type DeviceList struct {
	Video []DShowDevice `json:"video"`
	Audio []DShowDevice `json:"audio"`
}

var Config AppConfig
var AppVersion = "5.0.7-LINUX"
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
// 2. Configuration Management
// ==========================================
func getConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	return filepath.Join(homeDir, ".castor", "android_channels.json")
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
func getAdbPath() string {
	return "adb"
}

func getFFmpegPath() string {
	return "ffmpeg"
}

// ==========================================
// 4. ADB & Tuning Logic
// ==========================================
func ensureADBReady() {
	adb := getAdbPath()
	log.Println("Verifying ADB daemon availability...")

	for i := 1; i <= 10; i++ {
		cmd := exec.Command(adb, "start-server")
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
	connectCmd.Run()

	fullArgs := append([]string{"-s", deviceIP}, args...)
	cmd := exec.Command(adb, fullArgs...)

	return cmd.Run()
}

func checkADB(deviceIP string) bool {
	adb := getAdbPath()
	cmd := exec.Command(adb, "connect", deviceIP)
	
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	
	outStr := strings.ToLower(string(out))
	return strings.Contains(outStr, "connected")
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

	intentStr := fmt.Sprintf("%s/%s", provider.PackageName, provider.Component)
	adbCommand(deviceIP, "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", targetURL, "-n", intentStr)
}

// ==========================================
// 5. FFmpeg Hardware Discovery (Linux)
// ==========================================
func apiDevices(w http.ResponseWriter, r *http.Request) {
	devices := DeviceList{Video: []DShowDevice{}, Audio: []DShowDevice{}}

	// 1. Get Video Devices via v4l2-ctl
	cmdVid := exec.Command("v4l2-ctl", "--list-devices")
	outVid, _ := cmdVid.CombinedOutput()
	
	lines := strings.Split(string(outVid), "\n")
	var currentDeviceName string
	for _, line := range lines {
		if strings.HasSuffix(line, ":") {
			currentDeviceName = strings.TrimSuffix(line, ":")
		} else if strings.Contains(line, "/dev/video") && currentDeviceName != "" {
			devPath := strings.TrimSpace(line)
			// Only grab the first /dev/video path per device
			alreadyAdded := false
			for _, v := range devices.Video {
				if v.Name == currentDeviceName {
					alreadyAdded = true
					break
				}
			}
			if !alreadyAdded {
				devices.Video = append(devices.Video, DShowDevice{Name: currentDeviceName, ID: devPath})
			}
		}
	}

	// 2. Get Audio Devices via ALSA
	cmdAud := exec.Command("arecord", "-l")
	outAud, _ := cmdAud.CombinedOutput()
	
	linesAud := strings.Split(string(outAud), "\n")
	for _, line := range linesAud {
		if strings.HasPrefix(line, "card ") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				cardNumStr := strings.TrimSpace(strings.Split(parts[0], " ")[1])
				nameParts := strings.Split(parts[1], ",")
				devName := strings.TrimSpace(nameParts[0])
				
				// ALSA format requires hw:CARD,DEVICE
				hwPath := fmt.Sprintf("hw:%s,0", cardNumStr)
				devices.Audio = append(devices.Audio, DShowDevice{Name: devName, ID: hwPath})
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

	// Parse the multipart form, 10 MB max memory
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

	// Validate JSON structure before applying
	var tempConfig AppConfig
	if err := json.Unmarshal(body, &tempConfig); err != nil {
		http.Error(w, "Invalid JSON configuration", http.StatusBadRequest)
		return
	}

	// Apply and save
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

	executeTuning(tuner.DeviceIP, *channel)

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")

	// ==========================================
	// BRANCH A: Local USB Capture (Linux v4l2/alsa)
	// ==========================================
	if tuner.Type == "local" {
		time.Sleep(2 * time.Second)

		ffmpeg := getFFmpegPath()

		vCodec := tuner.VideoCodec
		if vCodec == "" {
			vCodec = "hevc_qsv"
		}
		vPreset := tuner.EncoderPreset
		if vPreset == "" {
			vPreset = "medium"
		}
		vBitrate := tuner.VideoBitrate
		if vBitrate == 0 {
			vBitrate = 2500
		}
		aBitrate := tuner.AudioBitrate
		if aBitrate == 0 {
			aBitrate = 128
		}

		vfArg := "format=nv12"
		if tuner.DeinterlaceMode == "tff" {
			vfArg = "bwdif=mode=1:parity=0,format=nv12"
		} else if tuner.DeinterlaceMode == "bff" {
			vfArg = "bwdif=mode=1:parity=1,format=nv12"
		}
		
		if strings.Contains(vCodec, "vaapi") {
			vfArg += ",hwupload"
		}
		vfArg += ",fps=59.94"

		args := []string{
			"-hide_banner", "-loglevel", "error",
		}

		// Inject hardware acceleration initialization depending on the selected codec
		if strings.Contains(vCodec, "qsv") {
			args = append(args, "-init_hw_device", "qsv=hw", "-filter_hw_device", "hw")
		} else if strings.Contains(vCodec, "vaapi") {
			args = append(args, "-init_hw_device", "vaapi=hw:/dev/dri/renderD128", "-filter_hw_device", "hw")
		}

		args = append(args,
			"-rtbufsize", "256M",
			"-thread_queue_size", "1024",
			"-f", "v4l2",
			"-i", tuner.VideoDeviceID,
			"-f", "alsa",
			"-i", tuner.AudioDeviceID,
			"-vf", vfArg,
			"-c:v", vCodec,
			"-preset", vPreset,
		)

		args = append(args,
			"-color_primaries", "bt709",
			"-color_trc", "bt709",
			"-colorspace", "bt709",
			"-color_range", "tv",
		)

		args = append(args, "-maxrate", fmt.Sprintf("%dk", vBitrate), "-bufsize", fmt.Sprintf("%dk", vBitrate*2))

		if tuner.AudioDelayMs > 0 {
			args = append(args, "-af", fmt.Sprintf("adelay=%d|%d", tuner.AudioDelayMs, tuner.AudioDelayMs))
		}

		args = append(args,
			"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", aBitrate), "-ar", "48000",
			"-f", "mpegts",
			"pipe:1",
		)

		cmd := exec.Command(ffmpeg, args...)

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

		streamChan := make(chan []byte, 500)

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

		for chunk := range streamChan {
			if _, err := w.Write(chunk); err != nil {
				break
			}
		}

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
			res.ADBOnline = checkADB(tuner.DeviceIP)

			if tuner.Type == "local" {
				// Simplified check for linux
				if _, err := exec.LookPath(getFFmpegPath()); err == nil {
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
	flag.Parse()

	loadConfig()

	if *uiFlag {
		localIP := getLocalIP()
		targetURL := fmt.Sprintf("http://%s:%d/status", localIP, Config.Port)

		// Run the browser launch in the background with a slight delay
		// so the HTTP server has time to start listening first.
		go func() {
			time.Sleep(1 * time.Second)
			cmd := exec.Command("xdg-open", targetURL)
			cmd.Start()
		}()
		// REMOVED the 'return' statement so the app continues starting
	}

	ensureADBReady()

	http.HandleFunc("/", statusPage)
	http.HandleFunc("/status", statusPage)
	http.HandleFunc("/health", healthCheck)
	http.HandleFunc("/api/config", apiConfig)
	http.HandleFunc("/api/export_config", apiExportConfig) // Added export endpoint
	http.HandleFunc("/api/import_config", apiImportConfig) // Added import endpoint
	http.HandleFunc("/api/devices", apiDevices)
	http.HandleFunc("/api/active_tuners", apiActiveTuners)
	http.HandleFunc("/stream/", streamHandler)
	http.HandleFunc("/channels.m3u", generateM3U)
	http.HandleFunc("/remote", remotePage)
	http.HandleFunc("/remote/keypress/", remoteKeypress)
	http.HandleFunc("/preview/", previewPage)
	http.HandleFunc("/api/check_tuners", checkTuners)
	http.HandleFunc("/api/release/", apiReleaseTuner)

	portString := fmt.Sprintf(":%d", Config.Port)
	
	log.Printf("ADB Bridge server listening on %s\n", portString)
	if err := http.ListenAndServe(portString, nil); err != nil {
		log.Fatalf("Server startup failed: %v\n", err)
	}
}