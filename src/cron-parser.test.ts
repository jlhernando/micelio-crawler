import { describe, it, expect } from 'vitest';
import { parseCron } from './cron-parser.js';

describe('parseCron field validation', () => {
  it.each([
    '1junk * * * *', // partial number
    '1junk/5 * * * *', // partial step base
    '0-100 * * * *', // range end out of bounds
    '0-100/5 * * * *', // stepped range out of bounds
    '10-5/2,1 * * * *', // descending stepped range hidden by another part
    '* 24 * * *',
    '* * 0 * *',
    '* * * 13 *',
    '* * * * 8',
    '1,,2 * * * *',
    '1/2/3 * * * *',
  ])('rejects %s', (expr) => {
    expect(() => parseCron(expr)).toThrow();
  });

  it('accepts valid expressions', () => {
    expect([...parseCron('*/15 0-5 1,15 * 1-5').minute.values]).toEqual([0, 15, 30, 45]);
    expect([...parseCron('5/20 * * * *').minute.values]).toEqual([5, 25, 45]);
    expect([...parseCron('0 0 * * 7').dayOfWeek.values]).toEqual([0]);
    expect([...parseCron('0 0 * * 5-7').dayOfWeek.values].sort()).toEqual([0, 5, 6]);
    expect([...parseCron('@daily').hour.values]).toEqual([0]);
  });
});
