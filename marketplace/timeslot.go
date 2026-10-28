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

// TierAt returns the billing tier for t. Peak/off-peak pricing was retired
// on 2026-10-28: every request is billed at the single flat price, so this
// always answers TierFlat. Historical rows keep the tier they were billed at.
func (c *Calendar) TierAt(t time.Time) string {
	return TierFlat
}

// NextSwitch reports the next tier flip. With a single flat tier there is
// none, so it returns the zero time (callers already treat that as unknown).
func (c *Calendar) NextSwitch(t time.Time) time.Time {
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
