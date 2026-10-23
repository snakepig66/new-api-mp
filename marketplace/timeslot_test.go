package marketplace

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// shanghaiTestLoc is the only billing timezone (§3.3). timeslot.go links the
// embedded tzdata, so this load cannot fail on any host.
var shanghaiTestLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return loc
}()

// shTime builds a deterministic Asia/Shanghai instant for table tests.
func shTime(y int, mo time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, mo, d, h, mi, s, 0, shanghaiTestLoc)
}

// newTestCalendar migrates an isolated database and loads a calendar from it
// (initially zero holiday rows).
func newTestCalendar(t *testing.T) (*gorm.DB, *Calendar) {
	t.Helper()
	db := newTestDB(t)
	migrateAll(t, db)
	cal, err := LoadCalendar(db)
	require.NoError(t, err)
	return db, cal
}

func TestTierAtWeekdayBoundarySeconds(t *testing.T) {
	_, cal := newTestCalendar(t)

	// The fixture date must be an ordinary Wednesday, otherwise the
	// expectations below test nothing.
	require.Equal(t, time.Wednesday, shTime(2026, time.October, 21, 0, 0, 0).Weekday(),
		"2026-10-21 must be a Wednesday")

	// §3.3 peak windows [09:00,12:00) and [14:00,18:00), half-open: the exact
	// second 12:00:00 / 18:00:00 is already off-peak (午休 / 晚间).
	cases := []struct {
		at   time.Time
		want string
	}{
		{shTime(2026, time.October, 21, 0, 0, 0), TierOffpeak},
		{shTime(2026, time.October, 21, 8, 59, 59), TierOffpeak},
		{shTime(2026, time.October, 21, 9, 0, 0), TierPeak},
		{shTime(2026, time.October, 21, 11, 59, 59), TierPeak},
		{shTime(2026, time.October, 21, 12, 0, 0), TierOffpeak},
		{shTime(2026, time.October, 21, 13, 59, 59), TierOffpeak},
		{shTime(2026, time.October, 21, 14, 0, 0), TierPeak},
		{shTime(2026, time.October, 21, 17, 59, 59), TierPeak},
		{shTime(2026, time.October, 21, 18, 0, 0), TierOffpeak},
		{shTime(2026, time.October, 21, 23, 59, 59), TierOffpeak},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, cal.TierAt(tc.at), "weekday %s", tc.at.Format("15:04:05"))
	}
}

func TestTierAtWeekendAllOffpeak(t *testing.T) {
	_, cal := newTestCalendar(t)

	days := []struct {
		day     int
		weekday time.Weekday
	}{
		{24, time.Saturday},
		{25, time.Sunday},
	}
	// Night, both would-be peak windows and the lunch break: every instant of
	// a weekend is off-peak (§3.3).
	times := [][3]int{{0, 0, 0}, {10, 0, 0}, {12, 30, 0}, {15, 0, 0}}
	for _, d := range days {
		require.Equal(t, d.weekday, shTime(2026, time.October, d.day, 0, 0, 0).Weekday(),
			"fixture: 2026-10-%d must be a %s", d.day, d.weekday)
		for _, tt := range times {
			at := shTime(2026, time.October, d.day, tt[0], tt[1], tt[2])
			assert.Equal(t, TierOffpeak, cal.TierAt(at),
				"%s %02d:%02d:%02d", d.weekday, tt[0], tt[1], tt[2])
		}
	}
}

func TestTierAtHoliday(t *testing.T) {
	db, cal := newTestCalendar(t)

	wednesday10 := shTime(2026, time.October, 21, 10, 0, 0)
	require.Equal(t, TierPeak, cal.TierAt(wednesday10), "baseline: Wednesday 10:00 is peak")

	require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-21", Kind: HolidayKindHoliday}}))
	assert.Equal(t, TierPeak, cal.TierAt(wednesday10),
		"the calendar is a snapshot: an import takes effect only after Reload")
	require.NoError(t, cal.Reload(db))

	// A 法定节假日 is off-peak all day, including both former peak windows.
	for _, tt := range [][3]int{{0, 0, 0}, {10, 0, 0}, {12, 30, 0}, {15, 0, 0}, {17, 59, 59}, {23, 59, 59}} {
		at := shTime(2026, time.October, 21, tt[0], tt[1], tt[2])
		assert.Equal(t, TierOffpeak, cal.TierAt(at), "holiday %02d:%02d:%02d", tt[0], tt[1], tt[2])
	}

	assert.Equal(t, TierPeak, cal.TierAt(shTime(2026, time.October, 22, 10, 0, 0)),
		"the neighbouring Thursday keeps its normal peak window")
}

func TestTierAtAdjustedWorkday(t *testing.T) {
	db, cal := newTestCalendar(t)

	// 2026-10-24 is a Saturday; 调休上班 makes it follow the weekday windows.
	require.Equal(t, time.Saturday, shTime(2026, time.October, 24, 0, 0, 0).Weekday())
	require.Equal(t, TierOffpeak, cal.TierAt(shTime(2026, time.October, 24, 10, 0, 0)),
		"baseline: Saturday 10:00 is off-peak")

	require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-24", Kind: HolidayKindWorkday}}))
	require.NoError(t, cal.Reload(db))

	cases := []struct {
		at   time.Time
		want string
	}{
		{shTime(2026, time.October, 24, 8, 59, 59), TierOffpeak},
		{shTime(2026, time.October, 24, 9, 0, 0), TierPeak},
		{shTime(2026, time.October, 24, 11, 59, 59), TierPeak},
		{shTime(2026, time.October, 24, 12, 0, 0), TierOffpeak}, // 午休 still applies
		{shTime(2026, time.October, 24, 13, 59, 59), TierOffpeak},
		{shTime(2026, time.October, 24, 14, 0, 0), TierPeak},
		{shTime(2026, time.October, 24, 17, 59, 59), TierPeak},
		{shTime(2026, time.October, 24, 18, 0, 0), TierOffpeak},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, cal.TierAt(tc.at), "调休上班周六 %s", tc.at.Format("15:04:05"))
	}
}

func TestTierAtKindPrecedence(t *testing.T) {
	db, cal := newTestCalendar(t)

	// One date carries exactly one kind, so the observable precedence is the
	// kind rule versus the plain weekday/weekend rule (§3.3): holiday beats
	// the weekday peak windows, workday beats the weekend off-peak rule.
	require.NoError(t, ImportHolidays(db, []Holiday{
		{Date: "2026-10-21", Kind: HolidayKindHoliday}, // Wednesday
		{Date: "2026-10-24", Kind: HolidayKindWorkday}, // Saturday
		{Date: "2026-10-25", Kind: HolidayKindHoliday}, // Sunday
	}))
	require.NoError(t, cal.Reload(db))

	assert.Equal(t, TierOffpeak, cal.TierAt(shTime(2026, time.October, 21, 10, 0, 0)),
		"holiday beats the Wednesday peak window")
	assert.Equal(t, TierOffpeak, cal.TierAt(shTime(2026, time.October, 21, 15, 0, 0)),
		"holiday beats the Wednesday afternoon window")
	assert.Equal(t, TierPeak, cal.TierAt(shTime(2026, time.October, 24, 10, 0, 0)),
		"workday beats the Saturday off-peak rule")
	assert.Equal(t, TierOffpeak, cal.TierAt(shTime(2026, time.October, 25, 10, 0, 0)),
		"holiday on a Sunday stays off-peak all day")
}

func TestTierAtNonShanghaiInput(t *testing.T) {
	_, cal := newTestCalendar(t)

	cases := []struct {
		name string
		at   time.Time
		want string
	}{
		// The same UTC day holds both tiers once converted to +08:00.
		{"UTC 01:00 is 09:00 Shanghai (peak)",
			time.Date(2026, time.October, 21, 1, 0, 0, 0, time.UTC), TierPeak},
		{"UTC 10:00 is 18:00 Shanghai (off-peak)",
			time.Date(2026, time.October, 21, 10, 0, 0, 0, time.UTC), TierOffpeak},
		// The Shanghai date, not the UTC date, drives the weekend lookup:
		// Friday 16:00 UTC is already Saturday 00:00 in Shanghai; judged in
		// UTC it would wrongly fall inside the Friday 14:00–18:00 window.
		{"UTC Friday 16:00 is already Saturday in Shanghai",
			time.Date(2026, time.October, 23, 16, 0, 0, 0, time.UTC), TierOffpeak},
		// A fixed-offset zone converts to the same Shanghai instant.
		{"UTC+9 11:00 is 10:00 Shanghai (peak)",
			time.Date(2026, time.October, 21, 11, 0, 0, 0, time.FixedZone("UTC+9", 9*3600)), TierPeak},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, cal.TierAt(tc.at))
		})
	}
}

func TestImportHolidaysValidation(t *testing.T) {
	t.Run("invalid date format rejected", func(t *testing.T) {
		db, _ := newTestCalendar(t)
		for _, date := range []string{"not-a-date", "2026-13-01", "2026-1-1", "2026/10/01"} {
			assert.Error(t, ImportHolidays(db, []Holiday{{Date: date, Kind: HolidayKindHoliday}}),
				"date %q must be rejected", date)
		}
	})

	t.Run("invalid kind rejected", func(t *testing.T) {
		db, _ := newTestCalendar(t)
		assert.Error(t, ImportHolidays(db, []Holiday{{Date: "2026-10-21", Kind: "weekend"}}))
		assert.Error(t, ImportHolidays(db, []Holiday{{Date: "2026-10-21", Kind: ""}}))
	})

	t.Run("one bad entry aborts the whole batch", func(t *testing.T) {
		db, _ := newTestCalendar(t)
		err := ImportHolidays(db, []Holiday{
			{Date: "2026-10-21", Kind: HolidayKindHoliday},
			{Date: "2026-13-01", Kind: HolidayKindHoliday},
		})
		require.Error(t, err)
		var cnt int64
		require.NoError(t, db.Model(&Holiday{}).Count(&cnt).Error)
		assert.Zero(t, cnt, "validation runs before any row is written")
	})

	t.Run("empty import is a no-op", func(t *testing.T) {
		db, _ := newTestCalendar(t)
		require.NoError(t, ImportHolidays(db, nil))
	})

	t.Run("re-importing a date updates its kind", func(t *testing.T) {
		db, cal := newTestCalendar(t)
		require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-24", Kind: HolidayKindHoliday}}))
		require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-24", Kind: HolidayKindWorkday}}))

		var rows []Holiday
		require.NoError(t, db.Find(&rows).Error)
		require.Len(t, rows, 1, "upsert must not duplicate the date row")
		assert.Equal(t, HolidayKindWorkday, rows[0].Kind, "the second import wins")

		require.NoError(t, cal.Reload(db))
		assert.Equal(t, TierPeak, cal.TierAt(shTime(2026, time.October, 24, 10, 0, 0)),
			"the Saturday now follows the workday windows")
	})
}
