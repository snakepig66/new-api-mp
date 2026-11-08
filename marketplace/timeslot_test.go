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

// TestTierAtAlwaysFlat: peak/off-peak pricing was retired on 2026-10-28, so
// every instant (weekday peak hours, weekends, holidays) bills at flat.
func TestTierAtAlwaysFlat(t *testing.T) {
	db, cal := newTestCalendar(t)
	require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-21", Kind: HolidayKindHoliday}}))
	require.NoError(t, cal.Reload(db))
	for _, at := range []time.Time{
		shTime(2026, time.October, 21, 10, 0, 0),
		shTime(2026, time.October, 22, 10, 0, 0),
		shTime(2026, time.October, 24, 3, 0, 0),
	} {
		assert.Equal(t, TierFlat, cal.TierAt(at))
		assert.True(t, cal.NextSwitch(at).IsZero())
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
		db, _ := newTestCalendar(t)
		require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-24", Kind: HolidayKindHoliday}}))
		require.NoError(t, ImportHolidays(db, []Holiday{{Date: "2026-10-24", Kind: HolidayKindWorkday}}))

		var rows []Holiday
		require.NoError(t, db.Find(&rows).Error)
		require.Len(t, rows, 1, "upsert must not duplicate the date row")
		assert.Equal(t, HolidayKindWorkday, rows[0].Kind, "the second import wins")

	})
}
