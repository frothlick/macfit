package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

type dashboardData struct {
	TotalCals     float64        `json:"totalCals"`
	RestingCals   float64        `json:"restingCals"`
	ActiveCals    float64        `json:"activeCals"`
	SessionActive float64        `json:"sessionActive"`
	BMR           float64        `json:"bmr"`
	Zone          string         `json:"zone"`
	ZoneColor     string         `json:"zoneColor"`
	ZoneEmoji     string         `json:"zoneEmoji"`
	MagEWMA       float64        `json:"magEWMA"`
	TotalEvents   int            `json:"totalEvents"`
	Sparkline     [60]float64    `json:"sparkline"`
	SparkIdx      int            `json:"sparkIdx"`
	Uptime        string         `json:"uptime"`
	MinuteLog     []MinuteRecord `json:"minuteLog"`
	Achievements  []string       `json:"achievements"`
	LatestAchieve string         `json:"latestAchievement"`
	History       []DayRecord    `json:"history"`
}

func startWebServer(state *CalorieState, hist *History, port int) {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(dashboardHTML))
	})

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		state.mu.Lock()
		data := buildDashboardData(state, hist)
		state.mu.Unlock()
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				state.mu.Lock()
				data := buildDashboardData(state, hist)
				state.mu.Unlock()
				jsonData, _ := json.Marshal(data)
				fmt.Fprintf(w, "data: %s\n\n", jsonData)
				flusher.Flush()
			}
		}
	})

	go func() {
		addr := fmt.Sprintf(":%d", port)
		_ = http.ListenAndServe(addr, mux)
	}()
}

func buildDashboardData(state *CalorieState, hist *History) dashboardData {
	elapsed := time.Since(state.StartTime)
	uptimeStr := fmt.Sprintf("%dh %dm", int(elapsed.Hours()), int(elapsed.Minutes())%60)

	var achieved []string
	for name := range state.Unlocked {
		achieved = append(achieved, name)
	}

	return dashboardData{
		TotalCals:     state.TotalCals,
		RestingCals:   state.RestingCals,
		ActiveCals:    state.ActiveCals,
		SessionActive: state.SessionActive,
		BMR:           state.Profile.BMR(),
		Zone:          state.CurrentZone.Name,
		ZoneColor:     state.CurrentZone.Color,
		ZoneEmoji:     state.CurrentZone.Emoji,
		MagEWMA:       state.MagEWMA,
		TotalEvents:   state.TotalEvents,
		Sparkline:     state.Sparkline,
		SparkIdx:      state.SparkIdx,
		Uptime:        uptimeStr,
		MinuteLog:     state.MinuteLog,
		Achievements:  achieved,
		LatestAchieve: state.LatestAchievement,
		History:       last7Days(hist),
	}
}

func openBrowser(port int) {
	url := fmt.Sprintf("http://localhost:%d", port)
	if runtime.GOOS == "darwin" {
		exec.Command("open", url).Start()
	}
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>MacFit - MacBook Fitness Tracker</title>
<script src="https://cdn.jsdelivr.net/npm/chart.js@4"></script>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, 'SF Pro Display', system-ui, sans-serif;
    background: #0a0a0f;
    color: #e0e0e0;
    min-height: 100vh;
  }
  .header {
    text-align: center;
    padding: 30px 20px 10px;
  }
  .header h1 {
    font-size: 2.5em;
    font-weight: 800;
    background: linear-gradient(135deg, #00ffaa, #00ccff);
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
    letter-spacing: 8px;
  }
  .header p { color: #666; font-size: 0.9em; margin-top: 4px; }
  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 16px;
    padding: 20px 30px;
    max-width: 1400px;
    margin: 0 auto;
  }
  .card {
    background: #14141f;
    border-radius: 16px;
    padding: 24px;
    border: 1px solid #222233;
  }
  .card h3 {
    font-size: 0.75em;
    text-transform: uppercase;
    letter-spacing: 2px;
    color: #666;
    margin-bottom: 12px;
  }
  .big-cal { text-align: center; }
  .big-cal .number {
    font-size: 3.5em;
    font-weight: 800;
    color: #ffdd00;
    line-height: 1;
  }
  .big-cal .unit { font-size: 1em; color: #999; margin-top: 4px; }
  .cal-breakdown {
    display: flex;
    justify-content: center;
    gap: 24px;
    margin-top: 16px;
    padding-top: 12px;
    border-top: 1px solid #222233;
  }
  .cal-breakdown .cal-item { text-align: center; }
  .cal-breakdown .cal-val { font-size: 1.4em; font-weight: 700; }
  .cal-breakdown .cal-label { font-size: 0.7em; color: #666; text-transform: uppercase; letter-spacing: 1px; }
  .resting-color { color: #6699AA; }
  .active-color { color: #00ffaa; }
  .zone-card { text-align: center; }
  .zone-name {
    font-size: 1.8em;
    font-weight: 700;
    margin: 8px 0;
    transition: color 0.3s;
  }
  .zone-emoji { font-size: 1.2em; }
  .intensity-bar {
    width: 100%;
    height: 12px;
    background: #1a1a2e;
    border-radius: 6px;
    overflow: hidden;
    margin-top: 12px;
  }
  .intensity-fill {
    height: 100%;
    border-radius: 6px;
    transition: width 0.5s ease, background 0.5s ease;
  }
  .stats-card .stat-row {
    display: flex;
    justify-content: space-between;
    padding: 8px 0;
    border-bottom: 1px solid #1a1a2e;
  }
  .stats-card .stat-row:last-child { border-bottom: none; }
  .stat-label { color: #666; }
  .stat-value { font-weight: 600; color: #00ffaa; }
  .chart-card {
    grid-column: 1 / -1;
    min-height: 280px;
  }
  .chart-card canvas { max-height: 240px; }
  .half-chart { grid-column: span 1; min-height: 250px; }
  .half-chart canvas { max-height: 200px; }
  .history-card { grid-column: 1 / 2; }
  .achieve-card { grid-column: 2 / -1; }
  .achieve-list { display: flex; flex-wrap: wrap; gap: 8px; }
  .achieve-badge {
    background: #1a1a0a;
    border: 1px solid #443300;
    color: #ffd700;
    padding: 6px 12px;
    border-radius: 20px;
    font-size: 0.85em;
    font-weight: 600;
  }
  .achieve-badge.locked {
    background: #111;
    border-color: #222;
    color: #333;
  }
  .history-bar-row {
    display: flex;
    align-items: center;
    margin: 6px 0;
  }
  .history-date { width: 60px; font-size: 0.8em; color: #666; }
  .history-bar-bg {
    flex: 1;
    height: 18px;
    background: #1a1a2e;
    border-radius: 4px;
    overflow: hidden;
    position: relative;
  }
  .history-bar-resting {
    height: 100%;
    background: #6699AA;
    position: absolute;
    left: 0;
    border-radius: 4px 0 0 4px;
  }
  .history-bar-active {
    height: 100%;
    background: #00ffaa;
    position: absolute;
    border-radius: 0 4px 4px 0;
  }
  .history-val { width: 80px; text-align: right; font-size: 0.8em; color: #999; }
  .quip {
    text-align: center;
    padding: 12px;
    color: #555;
    font-style: italic;
    font-size: 0.9em;
  }
  @media (max-width: 900px) {
    .grid { grid-template-columns: 1fr 1fr; }
    .chart-card, .half-chart { grid-column: 1 / -1; }
    .history-card { grid-column: 1 / -1; }
    .achieve-card { grid-column: 1 / -1; }
  }
</style>
</head>
<body>

<div class="header">
  <h1>M A C F I T</h1>
  <p>MacBook Fitness Tracker &mdash; <span id="bmrLabel">BMR: 0 kcal/day</span></p>
</div>

<div class="grid">
  <div class="card big-cal">
    <h3>Total Calories Today</h3>
    <div class="number" id="totalCal">0</div>
    <div class="unit">kcal</div>
    <div class="cal-breakdown">
      <div class="cal-item">
        <div class="cal-val resting-color" id="restingCal">0</div>
        <div class="cal-label">Resting</div>
      </div>
      <div class="cal-item">
        <div class="cal-val active-color" id="activeCal">0</div>
        <div class="cal-label">Active</div>
      </div>
    </div>
  </div>

  <div class="card zone-card">
    <h3>Activity Zone</h3>
    <div class="zone-emoji" id="zoneEmoji">[-]</div>
    <div class="zone-name" id="zoneName">Dead Weight</div>
    <div class="intensity-bar">
      <div class="intensity-fill" id="intensityFill"></div>
    </div>
  </div>

  <div class="card stats-card">
    <h3>Stats</h3>
    <div class="stat-row"><span class="stat-label">Session Active</span><span class="stat-value" id="sessionCal">0 cal</span></div>
    <div class="stat-row"><span class="stat-label">Bumps</span><span class="stat-value" id="bumps">0</span></div>
    <div class="stat-row"><span class="stat-label">Uptime</span><span class="stat-value" id="uptime">0h 0m</span></div>
    <div class="stat-row"><span class="stat-label">Resting rate</span><span class="stat-value" id="restingRate">0 cal/hr</span></div>
  </div>

  <div class="card chart-card">
    <h3>Calorie Timeline (per minute: resting + active)</h3>
    <canvas id="timelineChart"></canvas>
  </div>

  <div class="card half-chart">
    <h3>Live Activity (last 60 seconds)</h3>
    <canvas id="sparkChart"></canvas>
  </div>

  <div class="card half-chart">
    <h3>Resting vs Active</h3>
    <canvas id="donutChart"></canvas>
  </div>

  <div class="card history-card">
    <h3>7-Day History</h3>
    <div id="historyBars"></div>
    <div style="margin-top: 8px; font-size: 0.7em; color: #555;">
      <span style="color: #6699AA;">&#9632;</span> Resting &nbsp;
      <span style="color: #00ffaa;">&#9632;</span> Active
    </div>
  </div>

  <div class="card achieve-card">
    <h3>Achievements</h3>
    <div class="achieve-list" id="achievements"></div>
  </div>
</div>

<div class="quip" id="quip"></div>

<script>
const quips = {
  "Dead Weight": ["Your Mac is in a coma.", "Flatter than a pancake.", "Tim Cook is disappointed.", "Are you even there?"],
  "Barely Alive": ["A slight tremor in the force.", "Your desk has a heartbeat.", "Is that a breeze or your Mac moving?"],
  "Light Jiggle": ["A little shimmy never hurt.", "Your Mac is doing yoga.", "Gentle workout in progress."],
  "Active": ["Now we're cooking!", "Your Mac is on a brisk walk.", "Burning those silicon calories!"],
  "Intense": ["Your Mac is SPRINTING!", "SSD is praying for mercy.", "This is a HIIT workout!"],
  "EARTHQUAKE": ["SEISMOLOGISTS HAVE BEEN NOTIFIED.", "The Richter scale wants an apology.", "HOLD ON TO YOUR TRACKPAD!"]
};

const allAchievements = [
  "First Move", "Coffee Jitters", "Light Workout", "Power Walker",
  "Marathon Mac", "Earthquake Survivor", "Table Slapper", "Zen Master"
];

// Timeline chart - stacked bar (resting + active)
const timelineCtx = document.getElementById('timelineChart').getContext('2d');
const timelineChart = new Chart(timelineCtx, {
  type: 'bar',
  data: {
    labels: [],
    datasets: [
      {
        label: 'Resting',
        data: [],
        backgroundColor: 'rgba(102, 153, 170, 0.6)',
        borderWidth: 0,
        borderRadius: 0,
      },
      {
        label: 'Active',
        data: [],
        backgroundColor: 'rgba(0, 255, 170, 0.7)',
        borderWidth: 0,
        borderRadius: 3,
      }
    ]
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: 300 },
    plugins: {
      legend: { display: true, labels: { color: '#666', boxWidth: 12 } }
    },
    scales: {
      x: { stacked: true, grid: { color: '#1a1a2e' }, ticks: { color: '#555', maxTicksLimit: 20 } },
      y: { stacked: true, grid: { color: '#1a1a2e' }, ticks: { color: '#555' }, beginAtZero: true }
    }
  }
});

// Sparkline chart
const sparkCtx = document.getElementById('sparkChart').getContext('2d');
const sparkChart = new Chart(sparkCtx, {
  type: 'line',
  data: {
    labels: Array.from({length: 60}, (_, i) => (60 - i) + 's'),
    datasets: [{
      data: new Array(60).fill(0),
      borderColor: '#00ccff',
      backgroundColor: 'rgba(0, 204, 255, 0.1)',
      fill: true,
      tension: 0.3,
      pointRadius: 0,
      borderWidth: 2,
    }]
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: 300 },
    plugins: { legend: { display: false } },
    scales: {
      x: { grid: { color: '#1a1a2e' }, ticks: { color: '#555', maxTicksLimit: 10 } },
      y: { grid: { color: '#1a1a2e' }, ticks: { color: '#555' }, beginAtZero: true }
    }
  }
});

// Donut chart - resting vs active
const donutCtx = document.getElementById('donutChart').getContext('2d');
const donutChart = new Chart(donutCtx, {
  type: 'doughnut',
  data: {
    labels: ['Resting', 'Active'],
    datasets: [{
      data: [1, 0],
      backgroundColor: ['#6699AA', '#00ffaa'],
      borderWidth: 0,
    }]
  },
  options: {
    responsive: true,
    maintainAspectRatio: false,
    animation: { duration: 500 },
    plugins: {
      legend: { display: true, position: 'bottom', labels: { color: '#999', padding: 16 } }
    },
    cutout: '65%',
  }
});

let quipIdx = 0;

function update(data) {
  // BMR label
  document.getElementById('bmrLabel').textContent = 'BMR: ' + Math.round(data.bmr) + ' kcal/day';

  // Total calories
  document.getElementById('totalCal').textContent = Math.round(data.totalCals);
  document.getElementById('restingCal').textContent = Math.round(data.restingCals);
  document.getElementById('activeCal').textContent = Math.round(data.activeCals);

  // Zone
  document.getElementById('zoneName').textContent = data.zone;
  document.getElementById('zoneName').style.color = data.zoneColor;
  document.getElementById('zoneEmoji').textContent = data.zoneEmoji;

  // Intensity bar
  const pct = Math.min(100, data.magEWMA / 0.1 * 100);
  const fill = document.getElementById('intensityFill');
  fill.style.width = pct + '%';
  fill.style.background = data.zoneColor;

  // Stats
  document.getElementById('sessionCal').textContent = Math.round(data.sessionActive) + ' cal';
  document.getElementById('bumps').textContent = data.totalEvents;
  document.getElementById('uptime').textContent = data.uptime;
  document.getElementById('restingRate').textContent = (data.bmr / 24).toFixed(1) + ' cal/hr';

  // Sparkline
  const sparkData = [];
  for (let i = 0; i < 60; i++) {
    const idx = (data.sparkIdx + i) % 60;
    sparkData.push(data.sparkline[idx]);
  }
  sparkChart.data.datasets[0].data = sparkData;
  sparkChart.update('none');

  // Timeline - stacked resting + active per minute
  if (data.minuteLog && data.minuteLog.length > 0) {
    const labels = data.minuteLog.map(m => {
      const d = new Date(m.time);
      return d.getHours().toString().padStart(2, '0') + ':' + d.getMinutes().toString().padStart(2, '0');
    });
    timelineChart.data.labels = labels;
    timelineChart.data.datasets[0].data = data.minuteLog.map(m => m.restingCal);
    timelineChart.data.datasets[1].data = data.minuteLog.map(m => m.activeCal);
    timelineChart.update('none');
  }

  // Donut
  donutChart.data.datasets[0].data = [data.restingCals, data.activeCals];
  donutChart.update('none');

  // History bars - stacked resting + active
  const histDiv = document.getElementById('historyBars');
  if (data.history && data.history.length > 0) {
    const maxCal = Math.max(...data.history.map(d => d.totalCal), 1);
    histDiv.innerHTML = data.history.map(d => {
      const restPct = (d.restingCal / maxCal * 100).toFixed(1);
      const actPct = (d.activeCal / maxCal * 100).toFixed(1);
      const dateShort = d.date.slice(5);
      return '<div class="history-bar-row">' +
        '<span class="history-date">' + dateShort + '</span>' +
        '<div class="history-bar-bg">' +
          '<div class="history-bar-resting" style="width:' + restPct + '%"></div>' +
          '<div class="history-bar-active" style="left:' + restPct + '%;width:' + actPct + '%"></div>' +
        '</div>' +
        '<span class="history-val">' + Math.round(d.totalCal) + ' cal</span></div>';
    }).join('');
  }

  // Achievements
  const achieveDiv = document.getElementById('achievements');
  const unlocked = data.achievements || [];
  achieveDiv.innerHTML = allAchievements.map(a => {
    const isUnlocked = unlocked.includes(a);
    return '<span class="achieve-badge' + (isUnlocked ? '' : ' locked') + '">' + a + '</span>';
  }).join('');

  // Quip
  const zoneQuips = quips[data.zone] || quips["Dead Weight"];
  document.getElementById('quip').textContent = zoneQuips[quipIdx % zoneQuips.length];
}

// SSE
const evtSource = new EventSource('/api/events');
evtSource.onmessage = function(event) {
  update(JSON.parse(event.data));
};

setInterval(() => { quipIdx++; }, 5000);
fetch('/api/state').then(r => r.json()).then(update);
</script>
</body>
</html>`
