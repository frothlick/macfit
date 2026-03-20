package main

import (
	"math"
	"sync"
	"time"
)

// UserProfile holds the data needed to calculate BMR.
type UserProfile struct {
	WeightKg float64
	HeightCm float64
	Age      int
	Male     bool
}

// BMR calculates Basal Metabolic Rate using the Mifflin-St Jeor equation.
func (u UserProfile) BMR() float64 {
	bmr := 10*u.WeightKg + 6.25*u.HeightCm - 5*float64(u.Age)
	if u.Male {
		bmr += 5
	} else {
		bmr -= 161
	}
	return bmr
}

// BMRPerSecond returns the resting calorie burn rate.
func (u UserProfile) BMRPerSecond() float64 {
	return u.BMR() / 86400.0
}

type Zone struct {
	Name   string
	Emoji  string
	Color  string
	MinMag float64
}

var Zones = []Zone{
	{Name: "Dead Weight", Emoji: "[-]", Color: "#555555", MinMag: 0.000},
	{Name: "Barely Alive", Emoji: "[~]", Color: "#6699AA", MinMag: 0.002},
	{Name: "Light Jiggle", Emoji: "[o]", Color: "#44BB66", MinMag: 0.008},
	{Name: "Active", Emoji: "[*]", Color: "#FFAA00", MinMag: 0.020},
	{Name: "Intense", Emoji: "[!]", Color: "#FF5500", MinMag: 0.060},
	{Name: "EARTHQUAKE", Emoji: "[X]", Color: "#FF0000", MinMag: 0.200},
}

type Achievement struct {
	Name      string
	Message   string
	Condition func(s *CalorieState) bool
}

var achievements = []Achievement{
	{"First Move", "Your Mac twitched! It's alive!", func(s *CalorieState) bool { return s.ActiveCals > 0.1 }},
	{"Coffee Jitters", "Someone had an espresso...", func(s *CalorieState) bool { return s.ActiveCals > 10 }},
	{"Light Workout", "Your Mac went for a walk!", func(s *CalorieState) bool { return s.ActiveCals > 50 }},
	{"Power Walker", "Your Mac is getting fit!", func(s *CalorieState) bool { return s.ActiveCals > 150 }},
	{"Marathon Mac", "Your Mac ran a marathon!", func(s *CalorieState) bool { return s.ActiveCals > 500 }},
	{"Earthquake Survivor", "100 jolts survived. FEMA notified.", func(s *CalorieState) bool { return s.TotalEvents > 100 }},
	{"Table Slapper", "Your poor Mac. Please stop.", func(s *CalorieState) bool { return s.CurrentZone.Name == "EARTHQUAKE" }},
	{"Zen Master", "10 min of perfect stillness.", func(s *CalorieState) bool {
		return s.ActiveCals < 0.01 && time.Since(s.StartTime) > 10*time.Minute
	}},
}

type MinuteRecord struct {
	Time        time.Time `json:"time"`
	TotalCal    float64   `json:"totalCal"`
	ActiveCal   float64   `json:"activeCal"`
	RestingCal  float64   `json:"restingCal"`
	Events      int       `json:"events"`
	Zone        string    `json:"zone"`
	MagEWMA     float64   `json:"mag"`
}

type CalorieState struct {
	mu sync.Mutex

	Profile UserProfile

	// Calorie tracking (like a fitness tracker)
	RestingCals float64 // BMR-based, ticks with time
	ActiveCals  float64 // movement-based, from accelerometer
	TotalCals   float64 // Resting + Active

	// Session active calories (since app start)
	SessionActive float64

	CurrentZone Zone
	MagEWMA     float64

	Sparkline    [60]float64
	SparkIdx     int
	SparkSecCal  float64
	SparkLastSec time.Time

	TotalEvents int
	StartTime   time.Time
	LastBump    time.Time
	LastTick    time.Time // last time resting calories were updated

	Unlocked          map[string]bool
	LatestAchievement string
	AchievementTime   time.Time

	// Per-minute logging
	MinuteLog         []MinuteRecord
	MinuteActiveAcc   float64
	MinuteRestingAcc  float64
	MinuteEvtAcc      int
	MinuteLastTick    time.Time
}

func NewCalorieState(profile UserProfile) *CalorieState {
	now := time.Now()
	return &CalorieState{
		Profile:        profile,
		CurrentZone:    Zones[0],
		StartTime:      now,
		LastTick:       now,
		SparkLastSec:   now,
		MinuteLastTick: now.Truncate(time.Minute),
		Unlocked:       make(map[string]bool),
	}
}

// tickResting adds resting (BMR) calories based on elapsed wall-clock time.
// Caller must hold s.mu.
func (s *CalorieState) tickResting(now time.Time) {
	elapsed := now.Sub(s.LastTick).Seconds()
	if elapsed <= 0 {
		return
	}
	resting := s.Profile.BMRPerSecond() * elapsed
	s.RestingCals += resting
	s.TotalCals = s.RestingCals + s.ActiveCals
	s.MinuteRestingAcc += resting
	s.LastTick = now
}

// tickMinute checks if a minute boundary has passed and logs a record.
// Caller must hold s.mu.
func (s *CalorieState) tickMinute(now time.Time) {
	currentMinute := now.Truncate(time.Minute)
	if currentMinute.After(s.MinuteLastTick) {
		s.MinuteLog = append(s.MinuteLog, MinuteRecord{
			Time:       s.MinuteLastTick,
			TotalCal:   s.MinuteActiveAcc + s.MinuteRestingAcc,
			ActiveCal:  s.MinuteActiveAcc,
			RestingCal: s.MinuteRestingAcc,
			Events:     s.MinuteEvtAcc,
			Zone:       s.CurrentZone.Name,
			MagEWMA:    s.MagEWMA,
		})
		s.MinuteActiveAcc = 0
		s.MinuteRestingAcc = 0
		s.MinuteEvtAcc = 0
		s.MinuteLastTick = currentMinute
	}
}

func computeMag(ax, ay, az float64) float64 {
	raw := math.Sqrt(ax*ax + ay*ay + az*az)
	dynamic := raw - 1.0
	if dynamic < 0 {
		dynamic = 0
	}
	return dynamic
}

// Active calorie calculation.
//
// The sensor runs at 100 Hz, so there are 360,000 samples per hour.
// Even tiny per-sample values add up fast. The dead zone must be high
// enough to eliminate all sensor noise from a still laptop on a desk,
// and the scale must be low enough that only real movement produces
// meaningful calories.
//
// Calibration targets (with dead zone = 0.01g, scale = 1.0):
//   - Mac sitting perfectly still:       0 active cal/hr
//   - Normal desk, typing vibrations:    ~2-5 active cal/hr
//   - Picked up and carried around:      ~100-300 active cal/hr
//   - Shaken vigorously:                 ~5-15 cal per shake burst
//
// A typical day: 7hr desk work + 1hr moving = ~50-350 active cal.
// Combined with ~2020 kcal resting, total looks like a real tracker.
const magDeadZone = 0.01

const activeCalorieScale = 1.0

func deltaActiveCal(mag, dt float64) float64 {
	effective := mag - magDeadZone
	if effective <= 0 {
		return 0
	}
	return effective * dt * activeCalorieScale
}

func classifyZone(mag float64) Zone {
	best := Zones[0]
	for _, z := range Zones {
		if mag >= z.MinMag {
			best = z
		}
	}
	return best
}

func checkAchievements(s *CalorieState) {
	for _, a := range achievements {
		if s.Unlocked[a.Name] {
			continue
		}
		if a.Condition(s) {
			s.Unlocked[a.Name] = true
			s.LatestAchievement = a.Name + ": " + a.Message
			s.AchievementTime = time.Now()
		}
	}
}
