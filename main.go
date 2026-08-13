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
	Type            string `json:"type"` 
	EncoderURL      string `json:"encoder_url,omitempty"`
	VideoDeviceID   string `json:"video_device_id,omitempty"`
	AudioDeviceID   string `json:"audio_device_id,omitempty"`
	AudioDelayMs    int    `json:"audio_delay_ms,omitempty"`
	DeinterlaceMode string `json:"deinterlace_mode,omitempty"`
	CaptureFormat   string `json:"capture_format,omitempty"` // Automatically determined via FFmpeg probe
	CaptureSize     string `json:"capture_size,omitempty"`   // Automatically determined via FFmpeg probe
	CaptureFPS      string `json:"capture_fps,omitempty"`    // Automatically determined via FFmpeg probe
	Priority        int    `json:"priority"`
	InUse           bool   `json:"-"`
}

type Provider struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Intent        string `json:"intent,omitempty"` 
	PackageName   string `json:"package_name"`
	Component     string `json:"component"`
	URLTemplate   string `json:"url_template"`
	SplashDelayMs int    `json:"splash_delay_ms,omitempty"`
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

// Struct for the JSON response of the hardware probe
type ProbeResult struct {
	Format string `json:"format"`
	Size   string `json:"size"`
	FPS    string `json:"fps"`
}

var Config AppConfig
var AppVersion = "5.0.9-WIN"
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

func checkADB(deviceIP string) bool {
	adb := getAdbPath()
	cmd := exec.Command(adb, "connect", deviceIP)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

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
// 5. Hardware Diagnostics & Auto-Detection
// ==========================================

// getEncoderArgs queries the Windows OS for the GPU brand to assign a strict H.264 hardware encoder
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
			// Uses Intel's LA_ICQ for optimal visual fidelity
			return []string{"-c:v", "h264_qsv", "-look_ahead", "1", "-global_quality", "25"} 
		}
	}
	
	// Default to CPU fallback using standard Rec. 709 constraints for Channels DVR
	return []string{"-c:v", "libx264", "-preset", "veryfast", "-b:v", "8000k", "-bufsize", "16000k", "-pix_fmt", "yuv420p"}
}

// apiProbeDevice commands FFmpeg to list device pins and determines the best stream properties
func apiProbeDevice(w http.ResponseWriter, r *http.Request) {
	devName := r.URL.Query().Get("name")
	if devName == "" {
		http.Error(w, "Missing device name", http.StatusBadRequest)
		return
	}

	ffmpeg := getFFmpegPath()
	cmd := exec.Command(ffmpeg, "-f", "dshow", "-list_options", "true", "-i", fmt.Sprintf("video=%s", devName))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	
	// FFmpeg writes device lists directly to stderr and returns an error code
	out, _ := cmd.CombinedOutput()

	// Regex to capture "pixel_format=nv12 ... max s=1920x1080 fps=50" or "vcodec=mjpeg ... max s=1920x1080 fps=60"
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
			// 1080p outputs directly support Onn HD Stick rendering sizes natively
			if size == "1920x1080" {
				score += 100
			}

			// Prioritize compressed MJPEG first to sidestep USB 2.0 bandwidth drops
			if format == "mjpeg" {
				score += 50
			// NV12 avoids colorspace conversions before hitting Intel QSV 
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

	// Remove fractional zeros to keep the UI clean
	bestOption.FPS = strings.TrimSuffix(bestOption.FPS, ".000")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bestOption)
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
			if strings.Contains(line, "Alternative name") {
				continue
			}

			parts := strings.Split(line, "\"")
			if len(parts) >= 2 {
				val := parts[1]
				devType := currentType

				if strings.Contains(line, "(video)") {
					devType = "video"
				} else if strings.Contains(line, "(audio)") {
					devType = "audio"
				}

				newDev := DShowDevice{Name: val, ID: val}

				if devType == "video" {
					devices.Video = append(devices.Video, newDev)
				} else if devType == "audio" {
					devices.Audio = append(devices.Audio, newDev)
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

	executeTuning(tuner.DeviceIP, *channel)

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	// ==========================================
	// BRANCH A: Local USB Capture (Windows DirectShow)
	// ==========================================
	if tuner.Type == "local" {
		time.Sleep(2 * time.Second)

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

		// Retrieve dynamically assessed hardware limits
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

		// Inject Intel Hardware setup strictly if the auto-detected encoder is QSV
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
		
		// Apply strictly H.264 compatible hardware encoder limits determined during runtime WMI lookup
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

		for chunk := range streamChan {
			if _, err := w.Write(chunk); err != nil {
				break
			}
			if canFlush {
				flusher.Flush()
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
	flusher, canFlush := w.(http.Flusher)

	for {
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
	http.HandleFunc("/api/probe_device", apiProbeDevice) // Added probe endpoint
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