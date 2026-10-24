package marketplace

import (
	"fmt"
	"sync/atomic"
	"time"
	// Asia/Shanghai must resolve even on hosts without a system zoneinfo
	// database (scratch containers), so the embedded copy is linked in.
	_ "time/tzdata"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Holiday kinds stored in mp_holidays (§3.3): "holiday" is a 法定节假日
// (off-peak all day), "workday" is a 调休上班 weekend that follows weekday
// peak/off-peak windows.
const (
	HolidayKindHoliday = "holiday"
	HolidayKindWorkday = "workday"
)

// shanghaiTZ is the only timezone the marketplace bills in (§3.3).
const shanghaiTZ = "Asia/Shanghai"

// Calendar is the Asia/Shanghai peak/off-peak decider; its holiday calendar
// comes from mp_holidays. The day map is swapped atomically so Reload is safe
// to run from a background job while requests call TierAt.
type Calendar struct {
	loc  *time.Location
	days atomic.Pointer[map[string]string] // date "2006-01-02" -> kind
}

// LoadCalendar builds a Calendar and loads all rows of mp_holidays.
func LoadCalendar(db *gorm.DB) (*Calendar, error) {
	loc, err := time.LoadLocation(shanghaiTZ)
	if err != nil {
		return nil, fmt.Errorf("marketplace: load %s: %w", shanghaiTZ, err)
	}
	c := &Calendar{loc: loc}
	if err := c.Reload(db); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload re-reads the whole mp_holidays table and atomically swaps the day
// map. TierAt never sees a half-loaded calendar.
func (c *Calendar) Reload(db *gorm.DB) error {
	var rows []Holiday
	if err := db.Find(&rows).Error; err != nil {
		return fmt.Errorf("marketplace: load holidays: %w", err)
	}
	days := make(map[string]string, len(rows))
	for _, r := range rows {
		days[r.Date] = r.Kind
	}
	c.days.Store(&days)
	return nil
}

// TierAt classifies the instant t as TierPeak or TierOffpeak (§3.3):
//
//   - a "holiday" date is off-peak all day (holiday beats weekend rules);
//   - a "workday" date (调休上班) follows weekday windows even on a weekend;
//   - otherwise Saturday/Sunday is off-peak;
//   - any other weekday is peak inside [09:00,12:00) and [14:00,18:00)
//     (half-open: 12:00:00 and 18:00:00 are already off-peak).
func (c *Calendar) TierAt(t time.Time) string {
	lt := t.In(c.loc)
	kind := ""
	if days := c.days.Load(); days != nil {
		kind = (*days)[lt.Format("2006-01-02")]
	}
	switch kind {
	case HolidayKindHoliday:
		return TierOffpeak
	case HolidayKindWorkday:
		return weekdayTier(lt)
	}
	if wd := lt.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return TierOffpeak
	}
	return weekdayTier(lt)
}

// weekdayTier applies the Monday–Friday peak windows to a local time.
func weekdayTier(lt time.Time) string {
	sec := lt.Hour()*3600 + lt.Minute()*60 + lt.Second()
	if (sec >= 9*3600 && sec < 12*3600) || (sec >= 14*3600 && sec < 18*3600) {
		return TierPeak
	}
	return TierOffpeak
}

// NextSwitch returns the next instant after t at which the billing tier flips
// (高峰↔空闲, §3.3) — the model square's 下次切换时间 (§5 阶段 5). The holiday
// and 调休 calendar is honored through TierAt.
//
// Implementation: t is first truncated to the second; the scan then steps one
// minute at a time for at most 8 days looking for the first minute whose
// TierAt differs from TierAt(t), and walks that straddling minute one second
// at a time. Tier boundaries are at least an hour apart, so one minute holds
// at most one boundary, and every boundary (09:00 / 12:00 / 14:00 / 18:00 and
// holiday midnights) falls on a whole second — the result is therefore exact
// to the second, with the precision semantic "the first second of the new
// tier", and always strictly after t.
//
// The 8-day horizon covers the longest realistic unbroken stretch (a 7-day
// 法定节假日 plus an adjacent weekend). If no flip is found inside it the
// zero time is returned and callers must treat the switch time as unknown.
func (c *Calendar) NextSwitch(t time.Time) time.Time {
	t = t.Truncate(time.Second)
	base := c.TierAt(t)
	const maxScan = 8 * 24 * time.Hour
	prev := t
	for elapsed := time.Duration(0); elapsed < maxScan; elapsed += time.Minute {
		cur := prev.Add(time.Minute)
		if c.TierAt(cur) != base {
			for s := prev.Add(time.Second); ; s = s.Add(time.Second) {
				if c.TierAt(s) != base {
					return s
				}
			}
		}
		prev = cur
	}
	return time.Time{}
}

// ImportHolidays upserts holiday-calendar rows by Date (an existing date gets
// its Kind updated), so a year can be re-imported or corrected in place (§3.3).
func ImportHolidays(db *gorm.DB, entries []Holiday) error {
	for _, e := range entries {
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			return fmt.Errorf("marketplace: invalid holiday date %q: %w", e.Date, err)
		}
		if e.Kind != HolidayKindHoliday && e.Kind != HolidayKindWorkday {
			return fmt.Errorf("marketplace: invalid holiday kind %q for %s", e.Kind, e.Date)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "date"}},
		DoUpdates: clause.AssignmentColumns([]string{"kind"}),
	}).Create(&entries).Error
}
