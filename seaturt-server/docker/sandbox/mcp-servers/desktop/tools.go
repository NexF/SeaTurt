package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// getDisplay returns the X11 display. LinuxServer Webtop (Selkies) uses :1.
func getDisplay() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	return ":1"
}

func toolScreenshot(args map[string]any) CallToolResult {
	tmpFile := fmt.Sprintf("/tmp/screenshot_%d.png", time.Now().UnixNano())
	defer os.Remove(tmpFile)

	disp := getDisplay()
	var cmd *exec.Cmd

	// Check if a region is specified
	var regionOffX, regionOffY int
	if args != nil {
		if region, ok := args["region"].(map[string]any); ok {
			x := toInt(region["x"])
			y := toInt(region["y"])
			w := toInt(region["width"])
			h := toInt(region["height"])
			if w > 0 && h > 0 {
				regionOffX, regionOffY = x, y
				geometry := fmt.Sprintf("%dx%d+%d+%d", w, h, x, y)
				cmd = exec.Command("import", "-display", disp, "-window", "root",
					"-crop", geometry, tmpFile)
			}
		}
	}

	if cmd == nil {
		cmd = exec.Command("import", "-display", disp, "-window", "root", tmpFile)
	}

	cmd.Env = append(os.Environ(), "DISPLAY="+disp)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(fmt.Sprintf("screenshot failed: %v\n%s", err, string(output)))
	}

	// Overlay coordinate grid to help the LLM estimate click positions.
	// Controlled by "show_grid" param (default: true).
	showGrid := true
	if args != nil {
		if v, ok := args["show_grid"]; ok {
			switch g := v.(type) {
			case bool:
				showGrid = g
			case string:
				showGrid = g != "false" && g != "0"
			}
		}
	}

	if showGrid {
		gridFile := tmpFile + ".grid.png"
		defer os.Remove(gridFile)
		if err := overlayGrid(tmpFile, gridFile, regionOffX, regionOffY); err == nil {
			tmpFile = gridFile
		}
		// If grid overlay fails, fall through and return the plain screenshot.
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return errorResult(fmt.Sprintf("read screenshot: %v", err))
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return CallToolResult{
		Content: []ToolContent{{Type: "image", Data: encoded, MimeType: "image/png"}},
	}
}

// overlayGrid draws a coordinate grid on the screenshot using ImageMagick.
// gridStep controls the pixel interval between lines (default 100px).
// regionOffX/Y are the absolute desktop offsets of the captured region so
// that labels show real desktop coordinates the LLM can use directly.
func overlayGrid(srcPath, dstPath string, regionOffX, regionOffY int) error {
	const gridStep = 100

	// Get image dimensions via `identify`
	idOut, err := exec.Command("identify", "-format", "%w %h", srcPath).Output()
	if err != nil {
		return err
	}
	parts := strings.Fields(strings.TrimSpace(string(idOut)))
	if len(parts) != 2 {
		return fmt.Errorf("unexpected identify output: %s", string(idOut))
	}
	imgW, _ := strconv.Atoi(parts[0])
	imgH, _ := strconv.Atoi(parts[1])
	if imgW == 0 || imgH == 0 {
		return fmt.Errorf("zero dimension: %dx%d", imgW, imgH)
	}

	// Build ImageMagick draw commands:
	//  - semi-transparent grid lines every gridStep pixels
	//  - coordinate labels at intersections
	var draws []string

	// Grid lines (thin, semi-transparent)
	draws = append(draws, "stroke rgba(255,0,0,0.35)", "stroke-width 1", "fill none")
	for x := gridStep; x < imgW; x += gridStep {
		draws = append(draws, fmt.Sprintf("line %d,0 %d,%d", x, x, imgH))
	}
	for y := gridStep; y < imgH; y += gridStep {
		draws = append(draws, fmt.Sprintf("line 0,%d %d,%d", y, imgW, y))
	}

	// Coordinate labels at intersections
	draws = append(draws, "stroke none", "fill rgba(255,0,0,0.7)", "font-size 11")
	for x := gridStep; x < imgW; x += gridStep {
		for y := gridStep; y < imgH; y += gridStep {
			absX := x + regionOffX
			absY := y + regionOffY
			label := fmt.Sprintf("%d,%d", absX, absY)
			// Place label slightly offset from intersection
			draws = append(draws, fmt.Sprintf("text %d,%d '%s'", x+2, y-3, label))
		}
	}

	// Axis labels along edges (top row x, left col y)
	for x := gridStep; x < imgW; x += gridStep {
		absX := x + regionOffX
		draws = append(draws, fmt.Sprintf("text %d,12 '%d'", x+2, absX))
	}
	for y := gridStep; y < imgH; y += gridStep {
		absY := y + regionOffY
		draws = append(draws, fmt.Sprintf("text 2,%d '%d'", y-3, absY))
	}

	drawStr := strings.Join(draws, " ")
	cmd := exec.Command("convert", srcPath, "-draw", drawStr, dstPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(out))
	}
	return nil
}

func toolMouseClick(args map[string]any) CallToolResult {
	x := toInt(args["x"])
	y := toInt(args["y"])
	button := "1" // left button default

	if b, ok := args["button"].(string); ok {
		switch b {
		case "right":
			button = "3"
		case "middle":
			button = "2"
		default:
			button = "1"
		}
	}

	// Move to position first, then click
	disp := getDisplay()
	cmd := exec.Command("xdotool", "mousemove", "--sync",
		strconv.Itoa(x), strconv.Itoa(y), "click", button)
	cmd.Env = append(os.Environ(), "DISPLAY="+disp)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(fmt.Sprintf("mouse_click failed: %v\n%s", err, string(output)))
	}

	return textResult(fmt.Sprintf("clicked at (%d, %d) button=%s", x, y, button))
}

func toolMouseMove(args map[string]any) CallToolResult {
	x := toInt(args["x"])
	y := toInt(args["y"])

	disp := getDisplay()
	cmd := exec.Command("xdotool", "mousemove", "--sync",
		strconv.Itoa(x), strconv.Itoa(y))
	cmd.Env = append(os.Environ(), "DISPLAY="+disp)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(fmt.Sprintf("mouse_move failed: %v\n%s", err, string(output)))
	}

	return textResult(fmt.Sprintf("moved mouse to (%d, %d)", x, y))
}

func toolMouseDrag(args map[string]any) CallToolResult {
	fromX := toInt(args["from_x"])
	fromY := toInt(args["from_y"])
	toX := toInt(args["to_x"])
	toY := toInt(args["to_y"])

	// Move to start, press, move to end, release
	cmds := [][]string{
		{"xdotool", "mousemove", "--sync", strconv.Itoa(fromX), strconv.Itoa(fromY)},
		{"xdotool", "mousedown", "1"},
		{"xdotool", "mousemove", "--sync", strconv.Itoa(toX), strconv.Itoa(toY)},
		{"xdotool", "mouseup", "1"},
	}

	disp := getDisplay()
	for _, cmdArgs := range cmds {
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.Env = append(os.Environ(), "DISPLAY="+disp)
		if output, err := cmd.CombinedOutput(); err != nil {
			return errorResult(fmt.Sprintf("mouse_drag failed: %v\n%s", err, string(output)))
		}
	}

	return textResult(fmt.Sprintf("dragged from (%d,%d) to (%d,%d)", fromX, fromY, toX, toY))
}

func toolKeyboardType(args map[string]any) CallToolResult {
	text, ok := args["text"].(string)
	if !ok || text == "" {
		return errorResult("missing or invalid 'text' argument")
	}

	disp := getDisplay()
	cmd := exec.Command("xdotool", "type", "--clearmodifiers", "--delay", "50", text)
	cmd.Env = append(os.Environ(), "DISPLAY="+disp)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(fmt.Sprintf("keyboard_type failed: %v\n%s", err, string(output)))
	}

	return textResult(fmt.Sprintf("typed %d characters", len(text)))
}

func toolKeyboardKey(args map[string]any) CallToolResult {
	key, ok := args["key"].(string)
	if !ok || key == "" {
		return errorResult("missing or invalid 'key' argument")
	}

	// xdotool uses '+' for key combos (e.g. "ctrl+c")
	disp := getDisplay()
	cmd := exec.Command("xdotool", "key", "--clearmodifiers", key)
	cmd.Env = append(os.Environ(), "DISPLAY="+disp)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(fmt.Sprintf("keyboard_key failed: %v\n%s", err, string(output)))
	}

	return textResult(fmt.Sprintf("pressed key: %s", key))
}

func toolWindowList(args map[string]any) CallToolResult {
	disp := getDisplay()
	cmd := exec.Command("wmctrl", "-l")
	cmd.Env = append(os.Environ(), "DISPLAY="+disp)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errorResult(fmt.Sprintf("window_list failed: %v\n%s", err, string(output)))
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return textResult("(no windows found)")
	}
	return textResult(result)
}

func toolWindowFocus(args map[string]any) CallToolResult {
	disp := getDisplay()
	if windowID, ok := args["window_id"].(string); ok && windowID != "" {
		cmd := exec.Command("xdotool", "windowactivate", windowID)
		cmd.Env = append(os.Environ(), "DISPLAY="+disp)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return errorResult(fmt.Sprintf("window_focus by id failed: %v\n%s", err, string(output)))
		}
		return textResult(fmt.Sprintf("focused window: %s", windowID))
	}

	if title, ok := args["title"].(string); ok && title != "" {
		// Use wmctrl to find and activate by title
		cmd := exec.Command("wmctrl", "-a", title)
		cmd.Env = append(os.Environ(), "DISPLAY="+disp)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return errorResult(fmt.Sprintf("window_focus by title failed: %v\n%s", err, string(output)))
		}
		return textResult(fmt.Sprintf("focused window matching: %s", title))
	}

	return errorResult("either 'window_id' or 'title' is required")
}

func toolOpenApp(args map[string]any) CallToolResult {
	app, ok := args["app"].(string)
	if !ok || app == "" {
		return errorResult("missing or invalid 'app' argument")
	}

	disp := getDisplay()

	// Launch the app in a fully detached session (setsid) so it survives
	// independently. Capture stderr to a temp file so we can report
	// startup errors instead of silently swallowing them.
	errFile := fmt.Sprintf("/tmp/open_app_%d.err", time.Now().UnixNano())
	defer os.Remove(errFile)

	shellCmd := fmt.Sprintf(
		"setsid %s </dev/null >/dev/null 2>%s &\nSPID=$!\nsleep 1\nif ! kill -0 $SPID 2>/dev/null; then\n  cat %s\n  exit 1\nfi",
		app, errFile, errFile,
	)
	cmd := exec.Command("sh", "-c", shellCmd)
	cmd.Env = append(os.Environ(), "DISPLAY="+disp)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return errorResult(fmt.Sprintf("open_app failed: %s", msg))
	}

	return textResult(fmt.Sprintf("launched: %s", app))
}

func toolDesktopWait(args map[string]any) CallToolResult {
	delayMs := 1000
	if d, ok := args["delay_ms"]; ok {
		delayMs = toInt(d)
		if delayMs <= 0 {
			delayMs = 1000
		}
		if delayMs > 10000 {
			delayMs = 10000
		}
	}

	time.Sleep(time.Duration(delayMs) * time.Millisecond)

	// Take screenshot after waiting
	return toolScreenshot(nil)
}

// --- Helpers ---

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

func textResult(text string) CallToolResult {
	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: text}},
	}
}

func errorResult(msg string) CallToolResult {
	return CallToolResult{
		Content: []ToolContent{{Type: "text", Text: msg}},
		IsError: true,
	}
}
