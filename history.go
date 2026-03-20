package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type DayRecord struct {
	Date       string  `json:"date"`
	TotalCal   float64 `json:"totalCal"`
	RestingCal float64 `json:"restingCal"`
	ActiveCal  float64 `json:"activeCal"`
	Events     int     `json:"events"`
}

type History struct {
	Days []DayRecord `json:"days"`
}

func historyPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".macfit_history.json")
}

func loadHistory() *History {
	data, err := os.ReadFile(historyPath())
	if err != nil {
		return &History{}
	}
	var h History
	if err := json.Unmarshal(data, &h); err != nil {
		return &History{}
	}
	return &h
}

func saveHistory(h *History) {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return
	}
	tmp := historyPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return
	}
	os.Rename(tmp, historyPath())
}

func updateTodayRecord(h *History, state *CalorieState) {
	today := time.Now().Format("2006-01-02")
	for i := range h.Days {
		if h.Days[i].Date == today {
			h.Days[i].TotalCal = state.TotalCals
			h.Days[i].RestingCal = state.RestingCals
			h.Days[i].ActiveCal = state.ActiveCals
			h.Days[i].Events = state.TotalEvents
			return
		}
	}
	h.Days = append(h.Days, DayRecord{
		Date:       today,
		TotalCal:   state.TotalCals,
		RestingCal: state.RestingCals,
		ActiveCal:  state.ActiveCals,
		Events:     state.TotalEvents,
	})
}

func todayCaloriesFromHistory(h *History) (resting, active float64, events int) {
	today := time.Now().Format("2006-01-02")
	for _, d := range h.Days {
		if d.Date == today {
			return d.RestingCal, d.ActiveCal, d.Events
		}
	}
	return 0, 0, 0
}

func last7Days(h *History) []DayRecord {
	n := len(h.Days)
	if n <= 7 {
		return h.Days
	}
	return h.Days[n-7:]
}
