import { defineTheme } from '@astryxdesign/core/theme';
import { neutralTheme } from '@astryxdesign/theme-neutral';
import { siteTokens } from './site-tokens';

export const dashboardTheme = defineTheme({
  name: 'autorefresh',
  extends: neutralTheme,
  tokens: siteTokens,
});
