package scheduler

import (
	"log/slog"
	"testing"
	"time"
)

func TestSchedulerNextRun(t *testing.T) {
	s, err := New([]string{"03:00", "15:00"}, "UTC", slog.Default())
	if err != nil {
		t.Fatalf("New scheduler failed: %v", err)
	}

	next := s.NextRun()
	if next.IsZero() {
		t.Fatal("expected non-zero next run")
	}

	now := time.Now().UTC()
	if next.Before(now) {
		t.Errorf("expected next run %v to be after now %v", next, now)
	}

	formatted := s.NextRunFormatted()
	if formatted == "" || formatted == "Расписание не задано" {
		t.Errorf("unexpected formatted string: %s", formatted)
	}
}

func TestSchedulerInvalidTimes(t *testing.T) {
	_, err := New([]string{"25:00"}, "UTC", slog.Default())
	if err == nil {
		t.Error("expected error for invalid hour 25:00")
	}

	_, err = New([]string{"12:65"}, "UTC", slog.Default())
	if err == nil {
		t.Error("expected error for invalid minute 12:65")
	}

	_, err = New([]string{"12:00"}, "Invalid/Timezone", slog.Default())
	if err == nil {
		t.Error("expected error for invalid timezone")
	}
}
