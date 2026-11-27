import { AspectRatio } from '@astryxdesign/core/AspectRatio';
import { Badge } from '@astryxdesign/core/Badge';
import { Icon } from '@astryxdesign/core/Icon';
import { HStack, VStack } from '@astryxdesign/core/Layout';
import { colorVars, radiusVars, spacingVars } from '@astryxdesign/core/theme/tokens.stylex';
import { FilmIcon, TvIcon } from '@heroicons/react/24/outline';
import * as stylex from '@stylexjs/stylex';
import { useState } from 'react';
import { imageSize } from './api';
import { label } from './format';

// Poster widths in pixels. The photo transcoder sizes of the API are
// sm 120×180, md 240×360, and lg 480×720, so each width loads at least
// twice its size for sharp images on dense screens.
const posterWidth = { sm: 44, md: 56, lg: 160 } as const;
const posterSource = { sm: 'sm', md: 'sm', lg: 'md' } as const;

type PosterSize = keyof typeof posterWidth;

const styles = stylex.create({
  frame: {
    borderRadius: radiusVars['--radius-inner'],
    overflow: 'hidden',
    backgroundColor: colorVars['--color-background-muted'],
    flexShrink: 0,
  },
  sm: { width: posterWidth.sm },
  md: { width: posterWidth.md },
  lg: { width: posterWidth.lg, borderRadius: radiusVars['--radius-element'] },
  image: { width: '100%', height: '100%', objectFit: 'cover', display: 'block' },
  year: {
    position: 'absolute',
    insetInlineStart: spacingVars['--spacing-0-5'],
    insetBlockEnd: spacingVars['--spacing-0-5'],
  },
  backdrop: {
    position: 'relative',
    height: 220,
    overflow: 'hidden',
    backgroundColor: colorVars['--color-background-muted'],
  },
  backdropImage: {
    width: '100%',
    height: '100%',
    objectFit: 'cover',
    objectPosition: 'center 25%',
    display: 'block',
    opacity: 0.55,
  },
  backdropFade: {
    position: 'absolute',
    inset: 0,
    backgroundImage: `linear-gradient(to bottom, transparent 20%, ${colorVars['--color-background-surface']})`,
  },
});

interface PosterProps {
  src?: string;
  title?: string;
  year?: number;
  type?: string;
  size: PosterSize;
}

/** Shows the poster of an item at 2:3, with its year on top. Without a
 * poster, or when it does not load, it shows the icon of the item type. */
export function Poster({ src, title, year, type, size }: PosterProps) {
  const [failed, setFailed] = useState<string>();
  const url = imageSize(src, posterSource[size]);
  const showImage = url !== undefined && failed !== url;
  return (
    <AspectRatio ratio={2 / 3} xstyle={[styles.frame, styles[size]]}>
      {showImage ? (
        <img
          src={url}
          alt={title ? `Poster of ${title}` : ''}
          loading="lazy"
          decoding="async"
          onError={() => setFailed(url)}
          {...stylex.props(styles.image)}
        />
      ) : (
        <VStack hAlign="center" vAlign="center" height="100%">
          <Icon icon={type === 'show' ? TvIcon : FilmIcon} size={size === 'lg' ? 'lg' : 'sm'} color="secondary" />
        </VStack>
      )}
      {year && size !== 'sm' ? (
        <HStack xstyle={styles.year}>
          <Badge label={String(year)} />
        </HStack>
      ) : null}
    </AspectRatio>
  );
}

/** Shows the art of an item behind the head of its detail, faded into the
 * surface. Without art it shows a muted band. */
export function Backdrop({ src }: { src?: string }) {
  // The failure belongs to one URL: new art, as after a check, loads again.
  const [failed, setFailed] = useState<string>();
  const url = imageSize(src, 'md');
  return (
    <VStack xstyle={styles.backdrop} aria-hidden="true">
      {url && failed !== url ? <img src={url} alt="" onError={() => setFailed(url)} {...stylex.props(styles.backdropImage)} /> : null}
      <VStack xstyle={styles.backdropFade} />
    </VStack>
  );
}

const reasonColors: Record<string, 'red' | 'orange' | 'blue' | 'purple' | 'pink'> = {
  unmatched: 'red',
  mismatched: 'pink',
  'no-poster': 'orange',
  'no-art': 'orange',
  'no-summary': 'blue',
  'episode-no-summary': 'purple',
  'episode-unmatched': 'purple',
};

/** Shows the reasons of an item as coloured badges. Ignored reasons are
 * muted. */
export function Reasons({ reasons, ignored }: { reasons?: string[]; ignored: string[] }) {
  if (!reasons?.length) return null;
  return (
    <HStack gap={1} wrap="wrap">
      {reasons.map(reason => (
        <Badge
          key={reason}
          label={ignored.includes(reason) ? `${label(reason)} (ignored)` : label(reason)}
          variant={ignored.includes(reason) ? 'neutral' : reasonColors[reason] ?? 'neutral'}
        />
      ))}
    </HStack>
  );
}

/** Shows a remote poster of a match candidate, which Plex gave as an https
 * URL of a third-party host. The browser loads it without a referrer, and
 * without a poster it shows the icon of the type. */
export function RemotePoster({ src, title, type }: { src?: string; title?: string; type?: string }) {
  const [failed, setFailed] = useState<string>();
  const showImage = src !== undefined && failed !== src;
  return (
    <AspectRatio ratio={2 / 3} xstyle={[styles.frame, styles.md]}>
      {showImage ? (
        <img
          src={src}
          alt={title ? `Poster of ${title}` : ''}
          loading="lazy"
          decoding="async"
          referrerPolicy="no-referrer"
          onError={() => setFailed(src)}
          {...stylex.props(styles.image)}
        />
      ) : (
        <VStack hAlign="center" vAlign="center" height="100%">
          <Icon icon={type === 'show' ? TvIcon : FilmIcon} size="sm" color="secondary" />
        </VStack>
      )}
    </AspectRatio>
  );
}
