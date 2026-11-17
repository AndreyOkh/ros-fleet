package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Scheduler manages time-of-day execution of backup tasks.
type Scheduler struct {
	mu       sync.RWMutex
	times    []string
	timezone string
	loc      *time.Location
	logger   *slog.Logger
}

// New creates a new Scheduler with the specified run times (HH:MM) and timezone.
func New(times []string, timezone string, logger *slog.Logger) (*Scheduler, error) {
	s := &Scheduler{
		logger: logger.With(slog.String("component", "scheduler")),
	}

	if err := s.setScheduleLocked(times, timezone); err != nil {
		return nil, err
	}

	return s, nil
}

// Update updates the run times and timezone thread-safely.
func (s *Scheduler) Update(times []string, timezone string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setScheduleLocked(times, timezone)
}

func (s *Scheduler) setScheduleLocked(times []string, timezone string) error {
	var loc *time.Location
	var err error

	tzClean := strings.TrimSpace(timezone)
	if tzClean == "" || strings.EqualFold(tzClean, "Local") {
		loc = time.Local
	} else if strings.EqualFold(tzClean, "UTC") {
		loc = time.UTC
	} else {
		loc, err = time.LoadLocation(tzClean)
		if err != nil {
			return fmt.Errorf("invalid timezone %q: %w", tzClean, err)
		}
	}

	var validTimes []string
	for _, t := range times {
		tClean := strings.TrimSpace(t)
		if tClean == "" {
			continue
		}
		if _, _, err := parseTimeOfDay(tClean); err != nil {
			return fmt.Errorf("invalid schedule time %q: %w", tClean, err)
		}
		validTimes = append(validTimes, tClean)
	}

	s.times = validTimes
	s.timezone = tzClean
	s.loc = loc
	return nil
}

// NextRun computes the next trigger time. Returns time.IsZero() if no times configured.
func (s *Scheduler) NextRun() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.times) == 0 {
		return time.Time{}
	}

	now := time.Now().In(s.loc)
	var earliest time.Time

	for _, tStr := range s.times {
		h, m, _ := parseTimeOfDay(tStr)
		candidate := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, s.loc)
		if candidate.Before(now) || candidate.Equal(now) {
			candidate = candidate.AddDate(0, 0, 1)
		}

		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
	}

	return earliest
}

// NextRunFormatted returns a user-friendly string for the next run on the dashboard.
func (s *Scheduler) NextRunFormatted() string {
	next := s.NextRun()
	if next.IsZero() {
		return "Расписание не задано"
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().In(s.loc)
	diff := next.Sub(now)

	hours := int(diff.Hours())
	mins := int(diff.Minutes()) % 60

	if next.Day() == now.Day() {
		return fmt.Sprintf("Сегодня в %s (через %dч %dм)", next.Format("15:04"), hours, mins)
	}
	if next.Day() == now.AddDate(0, 0, 1).Day() {
		return fmt.Sprintf("Завтра в %s (через %dч %dм)", next.Format("15:04"), hours, mins)
	}

	return fmt.Sprintf("%s (через %dч %dм)", next.Format("02.01 15:04"), hours, mins)
}

// Times returns a copy of configured times.
func (s *Scheduler) Times() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.times))
	copy(res, s.times)
	return res
}

// Timezone returns the configured timezone string.
func (s *Scheduler) Timezone() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timezone
}

// Start runs the scheduler loop, triggering task at each scheduled time until ctx is done.
func (s *Scheduler) Start(ctx context.Context, task func(ctx context.Context)) {
	s.logger.Info("Starting time-of-day scheduler",
		slog.Any("times", s.Times()),
		slog.String("timezone", s.Timezone()),
	)

	for {
		next := s.NextRun()
		if next.IsZero() {
			s.logger.Warn("No valid schedule times configured; scheduler sleeping for 1 hour")
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Hour):
				continue
			}
		}

		wait := time.Until(next)
		s.logger.Info("Next scheduled backup",
			slog.Time("next_run", next),
			slog.Duration("wait_duration", wait.Round(time.Second)),
		)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			s.logger.Info("Scheduler terminating on context done")
			return
		case <-timer.C:
			s.logger.Info("Triggering scheduled backup task")
			task(ctx)
		}
	}
}

func parseTimeOfDay(s string) (int, int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, errors.New("must be in HH:MM format")
	}

	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, errors.New("hour must be between 00 and 23")
	}

	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, errors.New("minute must be between 00 and 59")
	}

	return h, m, nil
}
