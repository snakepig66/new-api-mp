import { describe, expect, it } from 'vitest'

import { parseHolidayInput } from './schemas'

describe('parseHolidayInput', () => {
  it('parses a JSON array and sorts by date', () => {
    const result = parseHolidayInput(
      '[{"date":"2027-10-09","kind":"workday"},{"date":"2027-10-01","kind":"holiday"}]',
    )
    expect(result.ok).toBe(true)
    if (!result.ok) return
    expect(result.entries).toEqual([
      { date: '2027-10-01', kind: 'holiday' },
      { date: '2027-10-09', kind: 'workday' },
    ])
  })

  it('parses line input with commas or whitespace and lets the last duplicate win', () => {
    const result = parseHolidayInput(
      ['# 2027 年安排', '2027-10-01, holiday', '2027-10-02 workday', ' 2027-10-01 , workday '].join(
        '\n',
      ),
    )
    expect(result.ok).toBe(true)
    if (!result.ok) return
    expect(result.entries).toEqual([
      { date: '2027-10-01', kind: 'workday' },
      { date: '2027-10-02', kind: 'workday' },
    ])
  })

  it('reports the line number and reason for an invalid kind', () => {
    const result = parseHolidayInput('2027-10-01, holiday\n2027-10-02, overtime')
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.error).toBe('第 2 行：类型只能是 holiday 或 workday')
  })

  it('rejects an invalid date with the item position in JSON mode', () => {
    const result = parseHolidayInput('[{"date":"2027-02-30","kind":"holiday"}]')
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.error).toBe('第 1 项：日期需为有效的 YYYY-MM-DD')
  })

  it('rejects malformed JSON, non-arrays and empty input', () => {
    expect(parseHolidayInput('[{"date":')).toEqual({
      ok: false,
      error: 'JSON 解析失败，请检查数组格式',
    })
    expect(parseHolidayInput('{"date":"2027-10-01","kind":"holiday"}')).toEqual({
      ok: false,
      error: 'JSON 必须是对象数组',
    })
    expect(parseHolidayInput('   \n  ')).toEqual({
      ok: false,
      error: '请粘贴要导入的日历数据',
    })
  })

  it('rejects a line with the wrong number of fields', () => {
    const result = parseHolidayInput('2027-10-01')
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.error).toBe('第 1 行：每行格式应为「YYYY-MM-DD, holiday|workday」')
  })
})
