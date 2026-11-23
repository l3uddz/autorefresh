import { describe, expect, it } from 'vitest';
import { freshReader } from './clock';
import { countdown, since } from './now';

const at = (time: string) => Date.parse(`2026-11-23T${time}Z`);

describe('second clock', () => {
  it('reads the time of a status event, not the time of the last tick', () => {
    let time = at('12:00:00.000');
    const read = freshReader(() => time);
    read(); // the tick
    time = at('12:00:00.700'); // the status event of the next item
    expect(countdown('2026-11-23T12:00:05.690Z', read())).toBe('in 5s');
  });

  it('never counts down to now or ages an item before the second passes', () => {
    let time = at('12:00:04.100');
    const read = freshReader(() => time);
    expect(countdown('2026-11-23T12:00:04.900Z', read())).toBe('in 1s');
    time = at('12:00:04.950');
    expect(since('2026-11-23T12:00:04.100Z', read())).toBe('now');
  });

  it('gives one time to the reads of one render', () => {
    let time = at('12:00:00.000');
    const read = freshReader(() => time);
    const first = read();
    time += 20;
    expect(read()).toBe(first);
  });
});
