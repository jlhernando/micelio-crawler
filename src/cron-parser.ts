/**
 * Lightweight 5-field cron expression parser.
 * Supports: minute hour day-of-month month day-of-week
 * Plus shortcuts: @hourly, @daily, @weekly, @monthly
 */

const SHORTCUTS: Record<string, string> = {
  '@yearly': '0 0 1 1 *',
  '@annually': '0 0 1 1 *',
  '@monthly': '0 0 1 * *',
  '@weekly': '0 0 * * 0',
  '@daily': '0 0 * * *',
  '@midnight': '0 0 * * *',
  '@hourly': '0 * * * *',
};

interface CronField {
  values: Set<number>;
  isWildcard: boolean; // true when the original field was * (all values)
}

export interface CronExpression {
  minute: CronField;
  hour: CronField;
  dayOfMonth: CronField;
  month: CronField;
  dayOfWeek: CronField;
  raw: string;
}

const FIELD_RANGES: [number, number][] = [
  [0, 59],   // minute
  [0, 23],   // hour
  [1, 31],   // day of month
  [1, 12],   // month
  [0, 6],    // day of week (0 = Sunday)
];

function parseField(field: string, min: number, max: number, isDow = false): CronField {
  const values = new Set<number>();
  let isWildcard = false;
  // Day-of-week accepts 7 as another name for Sunday (0)
  const maxInput = isDow ? 7 : max;

  // A whole-token number within the field's bounds ("1junk" or "100" in
  // the minute field are errors, not 1 or a silently clipped range)
  const num = (token: string, whole: string): number => {
    if (!/^\d+$/.test(token)) {
      throw new Error(`Invalid value "${whole}" (must be ${min}-${max})`);
    }
    const n = parseInt(token, 10);
    if (n < min || n > maxInput) {
      throw new Error(`Invalid value "${whole}" (must be ${min}-${max})`);
    }
    return n;
  };
  const add = (n: number) => values.add(isDow && n === 7 ? 0 : n);

  for (const part of field.split(',')) {
    const trimmed = part.trim();

    // Handle step: */5, 1-10/2 or 5/15 (from 5 to the end)
    const stepMatch = trimmed.match(/^(.+)\/(\d+)$/);
    if (stepMatch) {
      const [, range, stepStr] = stepMatch;
      const step = parseInt(stepStr, 10);
      if (isNaN(step) || step < 1) {
        throw new Error(`Invalid step value: ${stepStr}`);
      }

      let start = min;
      let end = max;

      if (range === '*') {
        isWildcard = true;
      } else {
        const rangeMatch = range.match(/^([^-]+)-([^-]+)$/);
        if (rangeMatch) {
          start = num(rangeMatch[1], trimmed);
          end = num(rangeMatch[2], trimmed);
          if (start > end) {
            throw new Error(`Invalid range: ${trimmed} (start > end)`);
          }
        } else {
          start = num(range, trimmed);
          end = max;
        }
      }

      for (let i = start; i <= end; i += step) add(i);
      continue;
    }

    if (trimmed.includes('/')) {
      throw new Error(`Invalid step expression "${trimmed}"`);
    }

    // Handle wildcard: *
    if (trimmed === '*') {
      isWildcard = true;
      for (let i = min; i <= max; i++) {
        values.add(i);
      }
      continue;
    }

    // Handle range: 1-5
    const rangeMatch = trimmed.match(/^([^-]+)-([^-]+)$/);
    if (rangeMatch) {
      const start = num(rangeMatch[1], trimmed);
      const end = num(rangeMatch[2], trimmed);
      if (start > end) {
        throw new Error(`Invalid range: ${trimmed} (start > end)`);
      }
      for (let i = start; i <= end; i++) add(i);
      continue;
    }

    // Handle single value
    add(num(trimmed, trimmed));
  }

  if (values.size === 0) {
    throw new Error(`Empty field after parsing`);
  }

  return { values, isWildcard };
}

export function parseCron(expression: string): CronExpression {
  const raw = expression.trim();

  // Handle shortcuts
  const resolved = SHORTCUTS[raw.toLowerCase()] || raw;

  const parts = resolved.split(/\s+/);
  if (parts.length !== 5) {
    throw new Error(
      `Invalid cron expression "${raw}": expected 5 fields (minute hour day-of-month month day-of-week), got ${parts.length}`,
    );
  }

  try {
    return {
      minute: parseField(parts[0], FIELD_RANGES[0][0], FIELD_RANGES[0][1]),
      hour: parseField(parts[1], FIELD_RANGES[1][0], FIELD_RANGES[1][1]),
      dayOfMonth: parseField(parts[2], FIELD_RANGES[2][0], FIELD_RANGES[2][1]),
      month: parseField(parts[3], FIELD_RANGES[3][0], FIELD_RANGES[3][1]),
      dayOfWeek: parseField(parts[4], FIELD_RANGES[4][0], FIELD_RANGES[4][1], true),
      raw,
    };
  } catch (err) {
    throw new Error(`Invalid cron expression "${raw}": ${(err as Error).message}`);
  }
}

/**
 * Calculate the next matching datetime after `from`.
 * Advances minute by minute, up to 4 years ahead (safety limit).
 */
export function nextRun(cron: CronExpression, from: Date = new Date()): Date {
  const next = new Date(from);
  // Start from the next minute
  next.setSeconds(0, 0);
  next.setMinutes(next.getMinutes() + 1);

  // Safety: search up to 4 years ahead
  const maxDate = new Date(from);
  maxDate.setFullYear(maxDate.getFullYear() + 4);

  while (next < maxDate) {
    // Check month (1-12)
    if (!cron.month.values.has(next.getMonth() + 1)) {
      // Skip to first day of next month
      next.setMonth(next.getMonth() + 1, 1);
      next.setHours(0, 0, 0, 0);
      continue;
    }

    // Check day of month (1-31) and day of week (0-6, Sunday = 0)
    // Standard cron: when BOTH fields are restricted (not wildcard), use OR logic.
    // When either is wildcard, use AND (effectively just checking the restricted one).
    const domMatch = cron.dayOfMonth.values.has(next.getDate());
    const dowMatch = cron.dayOfWeek.values.has(next.getDay());
    const bothRestricted = !cron.dayOfMonth.isWildcard && !cron.dayOfWeek.isWildcard;
    const dayMatch = bothRestricted ? (domMatch || dowMatch) : (domMatch && dowMatch);

    if (!dayMatch) {
      // Skip to next day
      next.setDate(next.getDate() + 1);
      next.setHours(0, 0, 0, 0);
      continue;
    }

    // Check hour (0-23)
    if (!cron.hour.values.has(next.getHours())) {
      // Skip to next hour
      next.setHours(next.getHours() + 1, 0, 0, 0);
      continue;
    }

    // Check minute (0-59)
    if (!cron.minute.values.has(next.getMinutes())) {
      // Skip to next minute
      next.setMinutes(next.getMinutes() + 1, 0, 0);
      continue;
    }

    // All fields match
    return next;
  }

  throw new Error(`No matching time found within 4 years for cron: ${cron.raw}`);
}

/**
 * Format a cron expression into a human-readable description.
 */
export function describeCron(expression: string): string {
  const lower = expression.trim().toLowerCase();
  if (lower === '@hourly') return 'Every hour';
  if (lower === '@daily' || lower === '@midnight') return 'Every day at midnight';
  if (lower === '@weekly') return 'Every Sunday at midnight';
  if (lower === '@monthly') return 'First day of every month at midnight';
  if (lower === '@yearly' || lower === '@annually') return 'January 1st at midnight';
  return `Cron: ${expression}`;
}
