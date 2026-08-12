# Simulated Keypress Tuning (Macros)

Not all streaming applications support "Deep Links" (URLs that launch directly into a specific live TV channel). For apps that force you to manually navigate menus or use an on-screen guide, the Android ADB Bridge utilizes **Simulated Keypress Tuning (Macros)**.

This system allows you to define a string of remote control commands that the bridge will execute sequentially over the network via ADB, effectively controlling the app for you.

---

## 1. The Tuning Sequence

When a channel is tuned, the bridge executes commands in a strict, five-step sequence. This allows you to "reset" an app's state before attempting to navigate its menus.

1. **Pre-Tune Macro (Provider):** Wakes the device and runs cleanup commands (e.g., pressing `BACK` or `HOME` to exit a current stream and return to a known menu state).
2. **App Launch (Intent):** The bridge brings the streaming app to the foreground.
3. **Splash Delay (Provider):** The system displays a black screen for `X` milliseconds to allow the application's splash screen to clear. Audio is muted during this phase.
4. **Channel Tuning Macro (Channel):** The sequence to actually select the requested channel (e.g., typing `1, 0, 4, ENTER` or navigating `DOWN:3, ENTER`).
5. **Post-Tune Macro (Provider):** Final cleanup commands to bring you to the desired start point for next tune (e.g., pressing `ENTER` to dismiss a lingering overlay or guide).

---

## 2. Supported Macro Commands

Macros are written as a comma-separated list of commands. Spaces are ignored.

### Action Commands
| Command | ADB Keyevent | Description |
| :--- | :--- | :--- |
| `UP` | 19 | D-Pad Up |
| `DOWN` | 20 | D-Pad Down |
| `LEFT` | 21 | D-Pad Left |
| `RIGHT` | 22 | D-Pad Right |
| `ENTER` / `OK` / `SELECT` | 23 | D-Pad Center / Select |
| `BACK` | 4 | Back / Exit Menu |
| `HOME` | 3 | Device Home Screen |
| `0` - `9` | 7 - 16 | Number pad keys |
| `PLAY` / `PAUSE` | 85 | Media Play/Pause |
| `FWD` / `REV` | 90 / 89 | Media Fast Forward / Rewind |
| `INFO` | 82 | Menu / Info |

### Delays
You can instruct the macro engine to pause execution using the `WAIT` or `SLEEP` commands, followed by a colon and the number of **milliseconds**.
* Example: `WAIT:1500` (Pauses for 1.5 seconds)

### Multipliers
If you need to press the same directional key multiple times, you can append a colon and the number of repetitions. The bridge will automatically insert a 200ms delay between consecutive presses to ensure the Android OS registers them properly.
* Example: `DOWN:5` (Presses Down 5 times)

---

## 3. Real-World Examples

**Example A: Numpad Tuning (Spectrum TV)**
The app supports typing numbers directly into the player to change channels.
* **Pre-Tune:** `HOME, WAIT:1000` (Ensures we aren't stuck in a sub-menu)
* **Tuning:** `1, 0, 4, ENTER` (Selects channel 104)
* **Post-Tune:** `WAIT:2000, ENTER` (Clears the info banner)

**Example B: Grid Guide Navigation**
The app has a vertical list of channels and no number pad support.
* **Pre-Tune:** `BACK:2, WAIT:1000` (Backs out of the current video to the guide)
* **Tuning:** `DOWN:4, ENTER` (Moves down 4 slots to the desired network)
