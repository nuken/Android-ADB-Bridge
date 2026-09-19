# ADB Tools & Device Management Guide

Android ADB Bridge includes a built-in diagnostic and maintenance toolkit directly in the web dashboard. These tools eliminate common headless tuner issues—such as devices falling asleep, storage filling up with streaming app caches, HDMI capture card handshake mismatches, and losing device authorization after server moves.

---

## 1. App Inspector (Detect App & Component)

Instead of manually searching for package names and activity components online, you can detect them directly from any running tuner.

### How to Use:
1. Turn on your streaming stick and launch the target streaming app using your physical remote (e.g., YouTube TV, Sling, DirecTV).
2. Open the **Android ADB Bridge** dashboard.
3. In the **Providers** section, click **Add Provider** (or click the pencil icon to edit an existing provider).
4. Click the purple **Detect from Device** button located above the Package Name field.
   * *If configuring Fire OS overrides:* Click **Fire TV Overrides** to expand the section, then click the orange **Detect Fire TV** button.
5. In the inspector modal, select the tuner currently running the app and click **Capture Screen & Intent**.
6. The bridge will pull a lightweight live screenshot so you can visually verify the screen, while simultaneously extracting the exact foreground `Package Name` and `Activity Component`.
7. Click **Apply to Provider** to automatically populate the form fields.
8. Click **Apply** on the Provider modal, then click **Save Now** on the floating banner to commit your changes.

---

## 2. Tuner ADB Tools (Wrench Icon)

Every tuner listed in the **Tuners** table features a green **Wrench icon** (`ADB Tools`) that opens a dedicated device utility menu.

### Power & System
* **Reboot Device:** Sends an instant reboot command to the selected streaming stick. Useful for clearing hung video buffers or memory leaks on low-RAM devices without pulling physical power cables.
* **Prevent Sleep ("Never Sleep"):** Configures the device to stay awake while powered (`stay_on_while_plugged_in 7`) and maxes out the display timeout (`screen_off_timeout 2147483647`). This prevents streaming sticks from entering sleep or ambient screen-saver modes that cut off HDMI capture output.
* **Trim Caches:** Instructs the Android package manager to flush temporary cache files across all installed applications without wiping user logins, passwords, or app data. Use this if apps like DirecTV or YouTube TV start throwing network congestion or storage-full errors.

### Display Overrides (Fix Capture Card Handshake Issues)
When connecting streaming sticks directly to USB HDMI capture dongles, the stick may fail to negotiate an EDID handshake, resulting in a black screen, distorted aspect ratios, or an unsupported 4K signal.

* **Lock 1080p:** Forces the Android Window Manager to render strictly at `1920x1080` with a standard density of `320 DPI` (`wm size 1920x1080` & `wm density 320`). This guarantees a compatible 1080p video feed for FFmpeg and local capture cards.
* **Auto-Detect (EDID):** Resets the resolution and density back to system defaults (`wm size reset` & `wm density reset`), allowing the stick to dynamically negotiate with connected hardware.

### Text Injector
Typing long Wi-Fi passwords, email addresses, or activation codes using an on-screen remote can be slow and tedious. 

1. On your TV, navigate to any active text input field.
2. Open the **ADB Tools** modal for that tuner.
3. Type or paste your text into the **Text Injector** box and click **Send**.
4. The text is immediately typed into the on-screen field over ADB.

---

## 3. ADB Key Vault (Backup & Restore Keys)

Android and Fire OS devices use a 2048-bit RSA key pair (`adbkey` and `adbkey.pub`) to identify authorized computers. When you check *"Always allow from this computer"* on your TV, the streaming stick permanently saves your server's public key.

To prevent keys from being lost when Windows updates or user profiles change, Android ADB Bridge stores the key pair in a shared system directory:
`C:\ProgramData\AndroidADBBridge\`

### Backup ADB Keys
Click the teal **Backup ADB Keys** button in the top action bar to download `adb_key_vault.zip` containing your `adbkey` and `adbkey.pub`. Store this file in a safe location.

### Restore ADB Keys
When migrating your setup to a new Windows machine or DVR capture server:
1. Install Android ADB Bridge on the new computer.
2. Click **Restore ADB Keys** on the dashboard.
3. Select your saved `adb_key_vault.zip` file.
4. The bridge unpacks the keys into the system vault and automatically restarts the ADB daemon.

Your streaming sticks will recognize the new installation immediately—**no on-screen authorization popups or physical remote clicks required.**