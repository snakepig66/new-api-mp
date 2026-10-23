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
