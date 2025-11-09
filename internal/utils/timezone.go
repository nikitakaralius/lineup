package utils

import (
	"fmt"
	"strings"
	"time"
)

var moscowLocation *time.Location

func init() {
	var err error
	moscowLocation, err = time.LoadLocation("Europe/Moscow")
	if err != nil {
		panic(fmt.Sprintf("failed to load Moscow timezone: %v", err))
	}
}

// ParseDurationInMoscow parses a duration string and returns the end time in Moscow timezone,
// then converts it to UTC for storage.
func ParseDurationInMoscow(durationStr string) (time.Duration, time.Time, error) {
	dur, err := time.ParseDuration(durationStr)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("invalid duration format: %w", err)
	}

	now := time.Now().In(moscowLocation)
	endsAtMoscow := now.Add(dur)
	endsAtUTC := endsAtMoscow.UTC()

	return dur, endsAtUTC, nil
}

// ParseEndTimeInMoscow parses an ISO 8601 datetime string in Moscow timezone and converts it to UTC.
// The LLM should provide end_time in ISO 8601 format: "2006-01-02T15:04:05+03:00"
func ParseEndTimeInMoscow(endTimeStr string) (time.Time, error) {
	endTimeStr = strings.TrimSpace(endTimeStr)

	// Parse ISO 8601 format with timezone
	if t, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
		// Convert to Moscow timezone if not already
		moscowTime := t.In(moscowLocation)
		return moscowTime.UTC(), nil
	}

	// Try ISO 8601 without timezone (assume Moscow)
	if t, err := time.Parse("2006-01-02T15:04:05", endTimeStr); err == nil {
		moscowTime := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, moscowLocation)
		return moscowTime.UTC(), nil
	}

	return time.Time{}, fmt.Errorf("неверный формат времени. Ожидается ISO 8601 формат (например, 2024-01-15T13:48:00+03:00), получено: %s", endTimeStr)
}

// FormatMoscowTimeForPoll formats a UTC time as Moscow timezone string for poll topic display.
// Format: "HH:MM DD.MM.YYYY MSK"
func FormatMoscowTimeForPoll(utcTime time.Time) string {
	moscowTime := utcTime.In(moscowLocation)
	return moscowTime.Format("15:04 02.01.2006 MSK")
}

// FormatMoscowTimeShort formats a UTC time as short Moscow timezone string (HH:MM).
func FormatMoscowTimeShort(utcTime time.Time) string {
	moscowTime := utcTime.In(moscowLocation)
	return moscowTime.Format("15:04")
}

// MoscowToUTC converts a Moscow time to UTC.
func MoscowToUTC(t time.Time) time.Time {
	if t.Location() == moscowLocation {
		return t.UTC()
	}
	// If time is already in UTC or another timezone, convert via Moscow
	moscowTime := t.In(moscowLocation)
	return moscowTime.UTC()
}
