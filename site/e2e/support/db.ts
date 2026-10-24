import { DatabaseSync } from 'node:sqlite'

import { DB_PATH } from './api'

/**
 * Direct SQLite access for local-mode fixtures only. The disposable backend
 * runs on a fresh DB under e2e/.tmp/run, so these writes cannot touch a real
 * deployment. Specs that need them skip when E2E_BASE_URL points elsewhere.
 */

export function localDbAvailable(): boolean {
  return Boolean(DB_PATH) && process.env.E2E_LOCAL === '1'
}

function open(): DatabaseSync {
  const db = new DatabaseSync(DB_PATH)
  db.exec('PRAGMA busy_timeout=30000')
  return db
}

function sqliteNow(): string {
  return new Date().toISOString().slice(0, 19).replace('T', ' ')
}

export function userId(username: string): number {
  const db = open()
  try {
    const row = db.prepare('SELECT id FROM users WHERE username = ?').get(username) as
      | { id: number }
      | undefined
    if (!row) throw new Error(`user ${username} not found in the local DB`)
    return Number(row.id)
  } finally {
    db.close()
  }
}

/** Buyer quota is an upstream integer; 1e9 is enough for the failed call. */
export function creditBuyer(username: string, quota = 1_000_000_000): void {
  const db = open()
  try {
    db.prepare('UPDATE users SET quota = ? WHERE username = ?').run(quota, username)
  } finally {
    db.close()
  }
}

/**
 * Waits for asynchronous verification to settle, then marks the listing live.
 * This replaces a real upstream key: the relay still fails at the vendor, but
 * the marketplace request path (and its usage record) is exercised end to end.
 */
export async function forceListingLive(listingId: number, timeoutMs = 90_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const db = open()
    try {
      const row = db.prepare('SELECT status FROM mp_listings WHERE id = ?').get(listingId) as
        | { status: string }
        | undefined
      const probe = db
        .prepare('SELECT COUNT(*) AS n FROM mp_probes WHERE listing_id = ?')
        .get(listingId) as { n: number } | undefined
      if (row && row.status !== 'verifying' && Number(probe?.n ?? 0) > 0) {
        db.prepare("UPDATE mp_listings SET status = 'live' WHERE id = ?").run(listingId)
        return
      }
    } finally {
      db.close()
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error(`listing ${listingId} did not finish verification in time`)
}

/** Seeds a withdrawable seller credit row so the payout path is reachable. */
export function creditSeller(username: string, amountMicro = 60_000_000): void {
  const sellerId = userId(username)
  const now = sqliteNow()
  const db = open()
  try {
    db.prepare(
      `INSERT INTO mp_ledger
         (request_id, entry_type, account, listing_id, amount_micro, price_version,
          multiplier_milli, status, available_at, created_at)
       VALUES (?, 'seller_credit', ?, 0, ?, 1, 900, 'available', ?, ?)`,
    ).run(`e2e-seed-${Date.now()}`, `seller:${sellerId}`, amountMicro, now, now)
  } finally {
    db.close()
  }
}
