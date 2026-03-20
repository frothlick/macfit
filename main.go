package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/taigrr/apple-silicon-accelerometer/detector"
	"github.com/taigrr/apple-silicon-accelerometer/sensor"
	"github.com/taigrr/apple-silicon-accelerometer/shm"
)

var quips = map[string][]string{
	"Dead Weight": {
		"Your Mac is in a coma.",
		"Flatter than a pancake.",
		"Tim Cook is disappointed.",
		"Are you even there?",
	},
	"Barely Alive": {
		"A slight tremor in the force.",
		"Your desk has a heartbeat.",
		"Is that a breeze or your Mac moving?",
	},
	"Light Jiggle": {
		"A little shimmy never hurt.",
		"Your Mac is doing yoga.",
		"Gentle workout in progress.",
	},
	"Active": {
		"Now we're cooking!",
		"Your Mac is on a brisk walk.",
		"Burning those silicon calories!",
	},
	"Intense": {
		"Your Mac is SPRINTING!",
		"SSD is praying for mercy.",
		"This is a HIIT workout!",
	},
	"EARTHQUAKE": {
		"SEISMOLOGISTS HAVE BEEN NOTIFIED.",
		"The Richter scale wants an apology.",
		"HOLD ON TO YOUR TRACKPAD!",
		"Your warranty just voided itself.",
	},
}

func main() {
	weightKg := flag.Float64("weight", 102, "Body weight in kg (for BMR calculation)")
	heightCm := flag.Float64("height", 200, "Height in cm (for BMR calculation)")
	age := flag.Int("age", 51, "Age in years (for BMR calculation)")
	male := flag.Bool("male", true, "Male (true) or female (false)")
	port := flag.Int("port", 8420, "Web dashboard port")
	flag.Parse()

	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "macfit requires root privileges for accelerometer access.")
		fmt.Fprintln(os.Stderr, "Run with: sudo macfit")
		os.Exit(1)
	}

	profile := UserProfile{
		WeightKg: *weightKg,
		HeightCm: *heightCm,
		Age:      *age,
		Male:     *male,
	}

	fmt.Fprintf(os.Stderr, "macfit: BMR = %.0f kcal/day (%.1f kcal/hr resting)\n",
		profile.BMR(), profile.BMR()/24)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	hist := loadHistory()
	state := NewCalorieState(profile)

	// Resume today's calories from history
	prevResting, prevActive, prevEvents := todayCaloriesFromHistory(hist)
	state.RestingCals = prevResting
	state.ActiveCals = prevActive
	state.TotalCals = prevResting + prevActive
	state.TotalEvents = prevEvents

	// Create shared memory ring buffer for accelerometer
	accelRing, err := shm.CreateRing(shm.NameAccel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating accel shm: %v\n", err)
		os.Exit(1)
	}
	defer accelRing.Close()
	defer accelRing.Unlink()

	// Start sensor worker
	sensorReady := make(chan struct{})
	sensorErr := make(chan error, 1)
	go func() {
		close(sensorReady)
		if err := sensor.Run(sensor.Config{
			AccelRing: accelRing,
			Restarts:  0,
		}); err != nil {
			sensorErr <- err
		}
	}()

	select {
	case <-sensorReady:
	case err := <-sensorErr:
		fmt.Fprintf(os.Stderr, "sensor failed: %v\n", err)
		os.Exit(1)
	case <-ctx.Done():
		return
	}

	time.Sleep(100 * time.Millisecond)

	// Start sensor polling loop
	go sensorLoop(ctx, state, accelRing)

	// Start resting calorie ticker (updates BMR burn every second)
	go restingLoop(ctx, state)

	// Start web dashboard
	startWebServer(state, hist, *port)
	fmt.Fprintf(os.Stderr, "macfit: dashboard at http://localhost:%d\n", *port)
	openBrowser(*port)

	// Auto-save history
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				state.mu.Lock()
				updateTodayRecord(hist, state)
				state.mu.Unlock()
				saveHistory(hist)
				return
			case <-ticker.C:
				state.mu.Lock()
				updateTodayRecord(hist, state)
				state.mu.Unlock()
				saveHistory(hist)
			}
		}
	}()

	// Enter alt screen
	fmt.Print("\033[?1049h") // enter alt screen
	fmt.Print("\033[?25l")   // hide cursor
	defer func() {
		fmt.Print("\033[?25h")   // show cursor
		fmt.Print("\033[?1049l") // exit alt screen
		// Final save
		state.mu.Lock()
		updateTodayRecord(hist, state)
		state.mu.Unlock()
		saveHistory(hist)
		fmt.Println("macfit: session saved. Stay active!")
	}()

	// Render loop
	renderTicker := time.NewTicker(200 * time.Millisecond)
	defer renderTicker.Stop()

	// Read keyboard input in background
	inputCh := make(chan byte, 1)
	go func() {
		buf := make([]byte, 1)
		oldState := enableRawMode()
		defer restoreTermMode(oldState)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				continue
			}
			select {
			case inputCh <- buf[0]:
			default:
			}
		}
	}()

	quipIdx := 0
	quipTicker := time.NewTicker(5 * time.Second)
	defer quipTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case b := <-inputCh:
			if b == 'q' || b == 'Q' || b == 3 {
				return
			}
		case <-quipTicker.C:
			quipIdx++
		case <-renderTicker.C:
			state.mu.Lock()
			frame := renderFrame(state, hist, quipIdx)
			state.mu.Unlock()
			fmt.Print("\033[H")
			fmt.Print(frame)
		}
	}
}

// restingLoop ticks BMR calories once per second.
func restingLoop(ctx context.Context, state *CalorieState) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			state.mu.Lock()
			state.tickResting(now)
			state.tickMinute(now)
			state.mu.Unlock()
		}
	}
}

func sensorLoop(ctx context.Context, state *CalorieState, ring *shm.RingBuffer) {
	det := detector.New()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastTotal uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		now := time.Now()
		tNow := float64(now.UnixNano()) / 1e9

		samples, newTotal := ring.ReadNew(lastTotal, shm.AccelScale)
		lastTotal = newTotal
		if len(samples) > 200 {
			samples = samples[len(samples)-200:]
		}

		nSamples := len(samples)
		for idx, s := range samples {
			tSample := tNow - float64(nSamples-idx-1)/float64(det.FS)
			det.Process(s.X, s.Y, s.Z, tSample)

			mag := computeMag(s.X, s.Y, s.Z)
			dt := 1.0 / float64(det.FS)
			activeCal := deltaActiveCal(mag, dt)

			state.mu.Lock()
			state.ActiveCals += activeCal
			state.SessionActive += activeCal
			state.TotalCals = state.RestingCals + state.ActiveCals
			state.MagEWMA = 0.05*mag + 0.95*state.MagEWMA
			state.CurrentZone = classifyZone(state.MagEWMA)

			// Sparkline (shows active cal per second)
			if now.Sub(state.SparkLastSec) >= time.Second {
				state.Sparkline[state.SparkIdx] = state.SparkSecCal
				state.SparkIdx = (state.SparkIdx + 1) % 60
				state.SparkSecCal = 0
				state.SparkLastSec = now
			}
			state.SparkSecCal += activeCal

			// Per-minute logging
			state.MinuteActiveAcc += activeCal
			state.mu.Unlock()
		}

		// Check for detector events
		if len(det.Events) > 0 {
			state.mu.Lock()
			state.TotalEvents += len(det.Events)
			state.MinuteEvtAcc += len(det.Events)
			state.LastBump = det.Events[len(det.Events)-1].Time
			checkAchievements(state)
			state.mu.Unlock()
			det.Events = det.Events[:0]
		}
	}
}

// renderFrame builds the full TUI frame. Caller must hold state.mu.
func renderFrame(state *CalorieState, hist *History, quipIdx int) string {
	width := 66

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00FFAA")).
		Width(width).
		Align(lipgloss.Center)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#444444")).
		Width(width).
		Padding(0, 1)

	calStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFDD00")).
		Width(width).
		Align(lipgloss.Center)

	zoneStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(state.CurrentZone.Color))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666"))

	greenStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00FFAA"))

	achieveStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFD700"))

	var b strings.Builder

	// Header
	b.WriteString(headerStyle.Render("M A C F I T"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Width(width).Align(lipgloss.Center).Render(
		fmt.Sprintf("MacBook Fitness Tracker  |  BMR: %.0f kcal/day", state.Profile.BMR())))
	b.WriteString("\n\n")

	// Big calorie number (total)
	calStr := fmt.Sprintf("%.0f kcal", state.TotalCals)
	b.WriteString(calStyle.Render(calStr))
	b.WriteString("\n")

	// Resting / Active breakdown
	breakdownLine := fmt.Sprintf("Resting: %.0f  +  Active: %s  =  Total",
		state.RestingCals,
		greenStyle.Render(fmt.Sprintf("%.0f", state.ActiveCals)))
	b.WriteString(dimStyle.Width(width).Align(lipgloss.Center).Render(breakdownLine))
	b.WriteString("\n\n")

	// Zone + stats
	zoneLine := fmt.Sprintf("  Zone: %s %s    Active: %.1f cal    Bumps: %d",
		zoneStyle.Render(state.CurrentZone.Name),
		state.CurrentZone.Emoji,
		state.SessionActive,
		state.TotalEvents)
	b.WriteString(zoneLine)
	b.WriteString("\n\n")

	// Intensity bar
	barWidth := 40
	filled := int(math.Min(float64(barWidth), state.MagEWMA/0.1*float64(barWidth)))
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", barWidth-filled)
	pct := math.Min(100, state.MagEWMA/0.1*100)
	b.WriteString(fmt.Sprintf("  Intensity: [%s] %.0f%%", bar, pct))
	b.WriteString("\n\n")

	// Sparkline (last 60 seconds, active cal only)
	sparkChars := []rune(" _.,:-=!#")
	var maxSpark float64
	for _, v := range state.Sparkline {
		if v > maxSpark {
			maxSpark = v
		}
	}
	if maxSpark < 0.01 {
		maxSpark = 0.01
	}
	var spark strings.Builder
	for i := 0; i < 60; i++ {
		idx := (state.SparkIdx + i) % 60
		v := state.Sparkline[idx]
		level := int(v / maxSpark * float64(len(sparkChars)-1))
		if level >= len(sparkChars) {
			level = len(sparkChars) - 1
		}
		if level < 0 {
			level = 0
		}
		spark.WriteRune(sparkChars[level])
	}
	b.WriteString(boxStyle.Render(fmt.Sprintf("  Active last 60s: [%s]", spark.String())))
	b.WriteString("\n")

	// History (last 7 days)
	days := last7Days(hist)
	if len(days) > 0 {
		var maxCal float64
		for _, d := range days {
			if d.TotalCal > maxCal {
				maxCal = d.TotalCal
			}
		}
		if maxCal < 1 {
			maxCal = 1
		}
		var histLines strings.Builder
		histLines.WriteString("  History:\n")
		barMax := 30
		for _, d := range days {
			bLen := int(d.TotalCal / maxCal * float64(barMax))
			if bLen < 0 {
				bLen = 0
			}
			dateShort := d.Date[5:]
			histBar := strings.Repeat("|", bLen) + strings.Repeat(" ", barMax-bLen)
			histLines.WriteString(fmt.Sprintf("  %s [%s] %.0f cal\n", dateShort, histBar, d.TotalCal))
		}
		b.WriteString(boxStyle.Render(histLines.String()))
		b.WriteString("\n")
	}

	// Achievement
	if state.LatestAchievement != "" && time.Since(state.AchievementTime) < 30*time.Second {
		b.WriteString(achieveStyle.Render(fmt.Sprintf("  >>> %s <<<", state.LatestAchievement)))
		b.WriteString("\n")
	} else if state.LatestAchievement != "" {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Last: %s", state.LatestAchievement)))
		b.WriteString("\n")
	}

	// Quip
	zoneQuips := quips[state.CurrentZone.Name]
	if len(zoneQuips) > 0 {
		b.WriteString("\n")
		b.WriteString(dimStyle.Width(width).Align(lipgloss.Center).Render(
			zoneQuips[quipIdx%len(zoneQuips)]))
	}

	// Footer
	elapsed := time.Since(state.StartTime)
	elapsedStr := fmt.Sprintf("%dh %dm", int(elapsed.Hours()), int(elapsed.Minutes())%60)
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Width(width).Align(lipgloss.Center).Render(
		fmt.Sprintf("Running: %s  |  Dashboard: localhost:8420  |  Q to quit", elapsedStr)))
	b.WriteString("\n")

	// Clear remaining lines
	lines := strings.Count(b.String(), "\n")
	for i := lines; i < 32; i++ {
		b.WriteString(strings.Repeat(" ", width))
		b.WriteString("\n")
	}

	return b.String()
}
