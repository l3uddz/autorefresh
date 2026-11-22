import { neutralTheme } from '@astryxdesign/theme-neutral';
import { describe, expect, it } from 'vitest';
import { siteTokens } from './site-tokens';

describe('dashboard theme', () => {
  it('only overrides tokens defined by Neutral', () => {
    expect(Object.keys(siteTokens).filter(key => !Object.hasOwn(neutralTheme.tokens, key))).toEqual([]);
  });
});
