package dockplugin

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// ParseDue turns what a person types into YYYY-MM-DD. Accepted: today,
// tomorrow (tom), a weekday (mon, friday: the next one), +N days, +Nw weeks,
// MM-DD, or a full date. Empty input means no date.
func ParseDue(in string, now time.Time) (string, error) {
	s := strings.ToLower(strings.TrimSpace(in))
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	switch s {
	case "":
		return "", nil
	case "today", "tod":
		return day(now), nil
	case "tomorrow", "tom":
		return day(now.AddDate(0, 0, 1)), nil
	}
	if len(s) >= 3 {
		if wd, ok := weekdays[s[:3]]; ok {
			n := (int(wd) - int(now.Weekday()) + 7) % 7
			if n == 0 {
				n = 7
			}
			return day(now.AddDate(0, 0, n)), nil
		}
	}
	if strings.HasPrefix(s, "+") {
		unit := 1
		num := s[1:]
		if strings.HasSuffix(num, "w") {
			unit, num = 7, strings.TrimSuffix(num, "w")
		}
		if n, err := strconv.Atoi(num); err == nil && n >= 0 {
			return day(now.AddDate(0, 0, n*unit)), nil
		}
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return day(t), nil
	}
	if t, err := time.Parse("01-02", s); err == nil {
		d := time.Date(now.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
		if d.Before(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())) {
			d = d.AddDate(1, 0, 0)
		}
		return day(d), nil
	}
	return "", fmt.Errorf("don't know the date %q (try today, tom, fri, +3, 12-24)", in)
}

// dueLabel renders a stored date relative to now and says how urgent it is:
// 2 overdue, 1 today, 0 otherwise.
func dueLabel(due string, now time.Time) (string, int) {
	t, err := time.ParseInLocation("2006-01-02", due, now.Location())
	if err != nil {
		return due, 0
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	n := int(t.Sub(today).Hours() / 24)
	switch {
	case n < 0:
		return fmt.Sprintf("%dd overdue", -n), 2
	case n == 0:
		return "today", 1
	case n == 1:
		return "tomorrow", 0
	case n < 7:
		return t.Format("Mon"), 0
	}
	return t.Format("Jan 2"), 0
}
